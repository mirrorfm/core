#!/usr/bin/python3.7

import os
import sys

# https://github.com/apex/apex/issues/639#issuecomment-455883587
file_path = os.path.dirname(__file__)
module_path = os.path.join(file_path, "env")
sys.path.append(module_path)

from pprint import pprint
from googleapiclient import discovery
from datetime import datetime, timezone, timedelta
import types

import dateutil.parser
import boto3
import pymysql
import json

import pytz
utc=pytz.UTC

# Hide warnings https://github.com/googleapis/google-api-python-client/issues/299
import logging
logging.getLogger('googleapiclient.discovery_cache').setLevel(logging.ERROR)

# DB
dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
mirrorfm_cursors = dynamodb.Table('mirrorfm_cursors')
mirrorfm_yt_tracks = dynamodb.Table('mirrorfm_yt_tracks')


name = os.getenv('DB_USERNAME')
password = os.getenv('DB_PASSWORD')
db_name = os.getenv('DB_NAME')
host = os.getenv('DB_HOST')


try:
    conn = pymysql.connect(host,
                           user=name,
                           passwd=password,
                           db=db_name,
                           connect_timeout=5,
                           cursorclass=pymysql.cursors.DictCursor)
except pymysql.MySQLError as e:
    print("ERROR: Unexpected error: Could not connect to MySQL instance.")
    print(e)
    sys.exit()

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
    next_last_upload_datetime = next_last_upload_datetime.replace(tzinfo=utc)
    item_datetime = get_datetime_from_iso8601_string(item['snippet']['publishedAt']).replace(tzinfo=utc)
    if item_datetime > last_upload_datetime.replace(tzinfo=utc):
        print(get_video_id(process_full_list, item['contentDetails']) + " " + item['snippet']['publishedAt'] + " - " + str(item['snippet']['title']))
        new_items_desc.append(item)
        if item_datetime > next_last_upload_datetime.replace(tzinfo=utc):
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


def get_next_channel():
    from_youtube_last_successful_channel = mirrorfm_cursors.get_item(
        Key={
            'name': 'from_youtube_last_successful_channel'
        },
        AttributesToGet=[
            'value'
        ]
    )

    cursor = conn.cursor()

    if 'Item' in from_youtube_last_successful_channel and from_youtube_last_successful_channel['Item']['value']:
        cursor.execute("SELECT * FROM yt_channels WHERE id=%s LIMIT 1" %
                       str(int(from_youtube_last_successful_channel['Item']['value']) + 1))
        channel = cursor.fetchone()
        if channel:
            return channel
    cursor.execute("SELECT * FROM yt_channels WHERE id=1")
    return cursor.fetchone()


def handle(event, context):
    upload_playlist_id = None
    last_upload_datetime = None
    old_yt_count_tracks = 0

    print("Event:", event)

    if 'Records' in event:
        # A channel_id was added to the `yt_channels` table
        print(event)
        channel_id = event['Records'][0]['Sns']['Message']
    else:
        # The lambda was triggered by CRON
        channel = get_next_channel()
        mirrorfm_cursors.put_item(
            Item={
                'name': 'from_youtube_last_successful_channel',
                'value': channel['id']
            }
        )
        channel_id = channel['channel_id']
        print(channel['channel_name'])
        if 'last_upload_datetime' in channel:
            last_upload_datetime = channel['last_upload_datetime']
        if 'count_tracks' in channel:
            old_yt_count_tracks = channel['count_tracks']

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

    try:
        pprint(response)
        upload_playlist_id = response['items'][0]['contentDetails']['relatedPlaylists']['uploads']
        channel_name = response['items'][0]['snippet']['title']
        thumbnails = response['items'][0]['snippet']['thumbnails']
    except KeyError as e:
        print(e)
        return
    except IndexError as e:
        print(e)
        # Ignore malformatted event / channel_id
        # It's likely the channel has been removed or terminated
        cur.execute("UPDATE yt_channels SET terminated_datetime = NOW() WHERE channel_id = %s",
                    [channel_id])
        conn.commit()
        return

    print(channel_name)

    cur = conn.cursor()
    thumbnail_high = thumbnails['high']['url']
    thumbnail_medium = thumbnails['medium']['url']
    thumbnail_default = thumbnails['default']['url']

    cur.execute("UPDATE yt_channels SET channel_name = %s, upload_playlist_id = %s, thumbnail_high = %s, thumbnail_medium = %s, thumbnail_default = %s, terminated_datetime = NULL WHERE channel_id = %s",
                [channel_name, upload_playlist_id, thumbnail_high, thumbnail_medium, thumbnail_default, channel_id])
    conn.commit()

    if not last_upload_datetime:
        process_full_list = True
        last_upload_datetime = datetime.min.replace(tzinfo=timezone.utc)
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
                return
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
                print(e)
                return
            for item in response['items']:
                if item['snippet']['type'] == 'upload':
                    next_last_upload_datetime = add_to_list_if_new_upload(item, new_items_desc, next_last_upload_datetime, last_upload_datetime, process_full_list)
            if 'nextPageToken' in response:
                page_token = response['nextPageToken']
            else:
                break

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
        cur = conn.cursor()
        cur.execute('UPDATE yt_channels SET last_upload_datetime = %s, count_tracks = %s WHERE channel_id = %s',
                    [next_last_upload_datetime.strftime('%Y-%m-%d %H:%M:%S'), count_tracks, channel_id])
        res = conn.commit()
        print(res)
    else:
        print("No new tracks")


# Quick local tests
if __name__ == "__main__":
    # Check next item (CRON mode)
    handle({}, {})

    # Add new channel
    # channel_id = "UCqTwKvjbTENZDGbz2si47ag"
    # handle({'Records': [{'eventID': '4fe2aab7e1e242ae10debd11ce811eb7', 'eventName': 'INSERT', 'eventVersion': '1.1', 'eventSource': 'aws:dynamodb', 'awsRegion': 'eu-west-1', 'dynamodb': {'ApproximateCreationDateTime': 1572478081.0, 'Keys': {'host': {'S': 'yt'}, 'channel_id': {'S': channel_id}}, 'NewImage': {'host': {'S': 'yt'}, 'channel_id': {'S': channel_id}}, 'SequenceNumber': '75150100000000017939913152', 'SizeBytes': 80, 'StreamViewType': 'NEW_AND_OLD_IMAGES'}, 'eventSourceARN': 'arn:aws:dynamodb:eu-west-1:705440408593:table/mirrorfm_channels/stream/2019-10-16T21:39:59.018'}]}, {})