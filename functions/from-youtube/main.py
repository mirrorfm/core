#!/usr/bin/python3.7

import os
import sys

# https://github.com/apex/apex/issues/639#issuecomment-455883587
file_path = os.path.dirname(__file__)
module_path = os.path.join(file_path, "env")
sys.path.append(module_path)

from pprint import pprint
from googleapiclient import discovery
from datetime import datetime
import types

import dateutil.parser
from zoneinfo import ZoneInfo

import boto3
import pymysql

# Hide warnings https://github.com/googleapis/google-api-python-client/issues/299
import logging
logging.getLogger('googleapiclient.discovery_cache').setLevel(logging.ERROR)

import json
import urllib.request
import xml.etree.ElementTree as ET

# DB
dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
sqs_client = boto3.client("sqs", region_name='eu-west-1')
SQS_TO_SPOTIFY_URL = os.getenv('SQS_TO_SPOTIFY_URL', '')
mirrorfm_cursors = dynamodb.Table('mirrorfm_cursors')
mirrorfm_yt_tracks = dynamodb.Table('mirrorfm_yt_tracks')


name = os.getenv('DB_USERNAME')
password = os.getenv('DB_PASSWORD')
db_name = os.getenv('DB_NAME')
host = os.getenv('DB_HOST')


conn = None


def get_conn():
    global conn
    if conn is None:
        conn = pymysql.connect(host=host,
                               user=name,
                               passwd=password,
                               db=db_name,
                               connect_timeout=5,
                               cursorclass=pymysql.cursors.DictCursor)
    else:
        conn.ping(reconnect=True)
    return conn

scopes = ["https://www.googleapis.com/auth/youtube.readonly"]


def yt_developer_keys():
    return [os.environ.get(env) for env in os.environ if env.startswith('YT_DEVELOPER_KEY')]


def get_datetime_from_iso8601_string(s):
    return dateutil.parser.parse(s)


def datetime_to_zulu(utc_datetime):
    return utc_datetime.strftime('%Y-%m-%dT%H:%M:%S.%fZ')


def chunks(l, n):
    """Yield successive n-sized chunks from l."""
    for i in range(0, len(l), n):
        yield l[i:i + n]


def get_video_id(playlist_item, item_content):
    if playlist_item:
        # playlist_item
        return item_content['videoId']
    else:
        # activity item
        return item_content['upload']['videoId']


def add_to_list_if_new_upload(item, new_items_desc, next_last_upload_datetime, last_upload_datetime, process_full_list):
    next_last_upload_datetime = next_last_upload_datetime.replace(tzinfo=ZoneInfo("UTC"))
    item_datetime = get_datetime_from_iso8601_string(item['snippet']['publishedAt']).replace(tzinfo=ZoneInfo("UTC"))
    if item_datetime > last_upload_datetime.replace(tzinfo=ZoneInfo("UTC")):
        # print(get_video_id(process_full_list, item['contentDetails']) + " " + item['snippet']['publishedAt'] + " - " + str(item['snippet']['title']))
        new_items_desc.append(item)
        if item_datetime > next_last_upload_datetime.replace(tzinfo=ZoneInfo("UTC")):
            return item_datetime
    return next_last_upload_datetime


def _flush(self):
    items_to_send = self._items_buffer[:self._flush_amount]
    self._items_buffer = self._items_buffer[self._flush_amount:]
    self._response = self._client.batch_write_item(
        RequestItems={self._table_name: items_to_send})
    unprocessed_items = self._response['UnprocessedItems']

    if unprocessed_items and unprocessed_items[self._table_name]:
        # Any unprocessed_items are immediately added to the
        # next batch we send.
        self._items_buffer.extend(unprocessed_items[self._table_name])
    else:
        self._items_buffer = []
    print("Batch write sent", len(items_to_send), "unprocessed:", len(self._items_buffer))


def fetch_rss_uploads(channel_id):
    """Fetch recent uploads via YouTube RSS feed (no API quota)."""
    url = f"https://www.youtube.com/feeds/videos.xml?channel_id={channel_id}"
    try:
        req = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'})
        with urllib.request.urlopen(req, timeout=15) as response:
            xml_data = response.read()
    except Exception as e:
        print(f"RSS fetch error for {channel_id}: {e}")
        return None

    root = ET.fromstring(xml_data)
    ns = {
        'atom': 'http://www.w3.org/2005/Atom',
        'yt': 'http://www.youtube.com/xml/schemas/2015',
    }

    items = []
    for entry in root.findall('atom:entry', ns):
        video_id = entry.find('yt:videoId', ns)
        title = entry.find('atom:title', ns)
        published = entry.find('atom:published', ns)
        if video_id is None or title is None or published is None:
            continue
        # Build an item structure compatible with the YouTube API format
        items.append({
            'snippet': {
                'publishedAt': published.text,
                'title': title.text,
                'type': 'upload',
            },
            'contentDetails': {
                'upload': {'videoId': video_id.text}
            }
        })
    return items


def get_next_channel():
    from_youtube_last_successful_channel = mirrorfm_cursors.get_item(
        Key={
            'name': 'from_youtube_last_successful_channel'
        },
        AttributesToGet=[
            'value'
        ]
    )

    if 'Item' in from_youtube_last_successful_channel and from_youtube_last_successful_channel['Item']['value']:
        last_channel_id = int(from_youtube_last_successful_channel['Item']['value'])
    else:
        last_channel_id = 1

    cursor = get_conn().cursor()
    cursor.execute("SELECT * FROM yt_channels WHERE (id > %s or id = 1) order by id = 1, id limit 1" % str(last_channel_id))
    return cursor.fetchone()


def get_channel(channel_id):
    cursor = get_conn().cursor()
    cursor.execute("SELECT * FROM yt_channels WHERE channel_id='%s'" % str(channel_id))
    return cursor.fetchone()


def handle(event, context):
    upload_playlist_id = None
    last_upload_datetime = None
    old_yt_count_tracks = 0

    if 'Records' in event:
        # A channel_id was added to the `yt_channels` table
        channel_id = event['Records'][0]['Sns']['Message']
        channel = get_channel(channel_id)
    else:
        # The lambda was triggered by CRON
        channel = get_next_channel()
        channel_id = channel['channel_id']
        print(channel['channel_name'])
        if 'last_upload_datetime' in channel:
            last_upload_datetime = channel['last_upload_datetime']
        if 'count_tracks' in channel:
            old_yt_count_tracks = channel['count_tracks']
    print(channel)
    print(last_upload_datetime)
    print(upload_playlist_id)
    print(old_yt_count_tracks)

    # Disable OAuthlib's HTTPS verification when running locally.
    # *DO NOT* leave this option enabled in production.
    os.environ["OAUTHLIB_INSECURE_TRANSPORT"] = "1"

    keys = yt_developer_keys()

    if len(keys) == 0:
        raise(Exception("Missing YT_DEVELOPER_KEY env"))

    for i, key in enumerate(keys):
        print(i, key)
        youtube = discovery.build("youtube", "v3", developerKey=key)

        try:
            response = youtube.channels().list(
                part="contentDetails,snippet",
                id=channel_id
            ).execute()
            break
        except Exception as e:
            print(i, key, e)
            if i == len(keys) - 1:
                raise(Exception("Quota exceeded on all developer keys"))

    cur = get_conn().cursor()
    try:
        upload_playlist_id = response['items'][0]['contentDetails']['relatedPlaylists']['uploads']
        channel_name = response['items'][0]['snippet']['title']
        thumbnails = response['items'][0]['snippet']['thumbnails']
    except (KeyError, IndexError) as e:
        # Ignore malformatted event / channel_id
        # It's likely the channel has been removed or terminated
        if not channel['terminated_datetime']:
            cur.execute("UPDATE yt_channels SET terminated_datetime = NOW() WHERE channel_id = %s",
                        [channel_id])
            get_conn().commit()
            print("Set channel as terminated")
        else:
            print("Channel already terminated")
        # Advance cursor past terminated channels
        if 'Records' not in event:
            mirrorfm_cursors.put_item(
                Item={
                    'name': 'from_youtube_last_successful_channel',
                    'value': channel['id']
                }
            )
        return {"searched": 1, "found": 0}

    print(channel_name)

    thumbnail_high = thumbnails['high']['url']
    thumbnail_medium = thumbnails['medium']['url']
    thumbnail_default = thumbnails['default']['url']

    cur.execute("UPDATE yt_channels SET channel_name = %s, upload_playlist_id = %s, thumbnail_high = %s, thumbnail_medium = %s, thumbnail_default = %s, terminated_datetime = NULL WHERE channel_id = %s",
                [channel_name, upload_playlist_id, thumbnail_high, thumbnail_medium, thumbnail_default, channel_id])
    get_conn().commit()

    if not last_upload_datetime or type(last_upload_datetime) == str:
        process_full_list = True
        old_yt_count_tracks = 0
        last_upload_datetime = datetime.min.replace(tzinfo=ZoneInfo("UTC"))
    else:
        process_full_list = False

    next_last_upload_datetime = last_upload_datetime
    page_token = ""
    new_items_desc = []

    if process_full_list:
        while True:
            try:
                response = youtube.playlistItems().list(
                    part="snippet,contentDetails",
                    playlistId=upload_playlist_id,
                    maxResults=50,
                    pageToken=page_token
                ).execute()
            except Exception as e:
                print(e)
                return {"searched": 1, "found": 0}
            for item in response['items']:
                next_last_upload_datetime = add_to_list_if_new_upload(item, new_items_desc, next_last_upload_datetime, last_upload_datetime, process_full_list)
            if 'nextPageToken' in response:
                page_token = response['nextPageToken']
            else:
                break
        # Reverse because YT API does not allow ASC
        # https://stackoverflow.com/a/22898075/1515819
        new_items_desc.reverse()
    else:
        # Use RSS feed for incremental updates (no API quota)
        rss_items = fetch_rss_uploads(channel_id)
        if rss_items is None:
            print("RSS fetch failed, skipping channel")
            return {"searched": 1, "found": 0}

        if len(rss_items) >= 15:
            # RSS maxes out at 15 — there may be more, fall back to API
            print(f"RSS returned {len(rss_items)} items (max), falling back to API")
            while True:
                try:
                    response = youtube.activities().list(
                        part="snippet,contentDetails",
                        channelId=channel_id,
                        maxResults=50,
                        pageToken=page_token,
                        publishedAfter=datetime_to_zulu(last_upload_datetime)
                    ).execute()
                except Exception as e:
                    print(f"API fallback failed: {e}")
                    return {"searched": 1, "found": 0}
                for item in response['items']:
                    if item['snippet']['type'] == 'upload':
                        next_last_upload_datetime = add_to_list_if_new_upload(item, new_items_desc, next_last_upload_datetime, last_upload_datetime, process_full_list)
                if 'nextPageToken' in response:
                    page_token = response['nextPageToken']
                else:
                    break
        else:
            for item in rss_items:
                if item['snippet']['type'] == 'upload':
                    next_last_upload_datetime = add_to_list_if_new_upload(item, new_items_desc, next_last_upload_datetime, last_upload_datetime, process_full_list)

    with mirrorfm_yt_tracks.batch_writer(overwrite_by_pkeys=["yt_channel_id", "yt_track_composite"]) as batch:
        batch._flush = types.MethodType(_flush, batch)
        for item in new_items_desc:
            track_id = get_video_id(process_full_list, item['contentDetails'])
            batch.put_item(
                Item={
                    'yt_channel_id': channel_id,
                    'yt_track_composite': '-'.join([str(item['snippet']['publishedAt']), track_id]),
                    'yt_track_id': track_id,
                    'yt_track_name': str(item['snippet']['title']),
                    'yt_published_at': item['snippet']['publishedAt']
                }
            )

    # Update channel row with last_upload_datetime
    print("next_last_upload_datetime", next_last_upload_datetime.strftime('%Y-%m-%d %H:%M:%S'))
    print("last_upload_datetime", last_upload_datetime)

    if next_last_upload_datetime and next_last_upload_datetime != last_upload_datetime:
        count_tracks = old_yt_count_tracks + len(new_items_desc)
        cur = get_conn().cursor()
        cur.execute('UPDATE yt_channels SET last_upload_datetime = %s, count_tracks = %s WHERE channel_id = %s',
                    [next_last_upload_datetime.strftime('%Y-%m-%d %H:%M:%S'), count_tracks, channel_id])
        res = get_conn().commit()
        print(res)
        # Notify to-spotify to process this channel's tracks
        if SQS_TO_SPOTIFY_URL and len(new_items_desc) > 0:
            sqs_client.send_message(
                QueueUrl=SQS_TO_SPOTIFY_URL,
                MessageBody=json.dumps({"host": "yt", "entity_id": channel_id}))
            print("Notified to-spotify via SQS")

        # Advance cursor only after successful processing
        if 'Records' not in event:
            mirrorfm_cursors.put_item(
                Item={
                    'name': 'from_youtube_last_successful_channel',
                    'value': channel['id']
                }
            )
        return {"searched": 1, "found": len(new_items_desc)}
    else:
        print("No new tracks")
        # Advance cursor only after successful processing
        if 'Records' not in event:
            mirrorfm_cursors.put_item(
                Item={
                    'name': 'from_youtube_last_successful_channel',
                    'value': channel['id']
                }
            )
        return {"searched": 1, "found": 0}


# Quick local tests
if __name__ == "__main__":
    # Check next item (CRON mode)
    handle({}, {})

    # Add new channel
    # channel_id = "UCqTwKvjbTENZDGbz2si47ag"
    # handle({'Records': [{'eventID': '4fe2aab7e1e242ae10debd11ce811eb7', 'eventName': 'INSERT', 'eventVersion': '1.1', 'eventSource': 'aws:dynamodb', 'awsRegion': 'eu-west-1', 'dynamodb': {'ApproximateCreationDateTime': 1572478081.0, 'Keys': {'host': {'S': 'yt'}, 'channel_id': {'S': channel_id}}, 'NewImage': {'host': {'S': 'yt'}, 'channel_id': {'S': channel_id}}, 'SequenceNumber': '75150100000000017939913152', 'SizeBytes': 80, 'StreamViewType': 'NEW_AND_OLD_IMAGES'}, 'eventSourceARN': 'arn:aws:dynamodb:eu-west-1:705440408593:table/mirrorfm_channels/stream/2019-10-16T21:39:59.018'}]}, {})
