#!/usr/bin/python3.6

import os
import sys

# https://github.com/apex/apex/issues/639#issuecomment-455883587
file_path = os.path.dirname(__file__)
module_path = os.path.join(file_path, "env")
sys.path.append(module_path)

from pprint import pprint
from googleapiclient import discovery
from datetime import datetime, timezone, timedelta
import time

import dateutil.parser
import boto3
from boto3.dynamodb.conditions import Key, Attr
from botocore.exceptions import ClientError

# Hide warnings https://github.com/googleapis/google-api-python-client/issues/299
import logging
logging.getLogger('googleapiclient.discovery_cache').setLevel(logging.ERROR)

# DB
dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
mirrorfm_channels = dynamodb.Table('mirrorfm_channels')
mirrorfm_cursors = dynamodb.Table('mirrorfm_cursors')

scopes = ["https://www.googleapis.com/auth/youtube.readonly"]

YT_DEVELOPER_KEY = os.getenv('YT_DEVELOPER_KEY')


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
    item_datetime = get_datetime_from_iso8601_string(item['snippet']['publishedAt'])
    if item_datetime > last_upload_datetime:
        print(get_video_id(process_full_list, item['contentDetails']) + " " + item['snippet']['publishedAt'] + " - " + str(item['snippet']['title']))
        new_items_desc.append(item)
        if item_datetime > next_last_upload_datetime:
            return item_datetime
    return next_last_upload_datetime


def handle(event, context):
    upload_playlist_id = None
    last_upload_datetime = None
    old_yt_count_tracks = 0
    print(event)
    if 'Records' in event:
        # A channel_id was added to the `mirrorfm_channels` dynamodb table
        if event['Records'][0]['eventName'] != "INSERT":
            # Ignore row updates
            return
        print(event)
        channel_id = event['Records'][0]['dynamodb']['Keys']['channel_id']['S']
    else:
        # The lambda was triggered by CRON
        exclusive_start_yt_channel_key = mirrorfm_cursors.get_item(
            Key={
                'name': 'exclusive_start_yt_channel_key'
            },
            AttributesToGet=[
                'value'
            ]
        )

        if 'Item' in exclusive_start_yt_channel_key and exclusive_start_yt_channel_key['Item'] != {}:
            channel_info = mirrorfm_channels.query(
                Limit=1,
                ExclusiveStartKey=exclusive_start_yt_channel_key['Item']['value'],
                KeyConditionExpression=Key('host').eq('yt'))
        else:
            # no cursor, query first
            channel_info = mirrorfm_channels.query(
                Limit=1,
                KeyConditionExpression=Key('host').eq('yt'))

        # Only do this if successful
        if 'LastEvaluatedKey' in channel_info:
            exclusive_start_yt_channel_key = channel_info['LastEvaluatedKey']
            mirrorfm_cursors.put_item(
                Item={
                    'name': 'exclusive_start_yt_channel_key',
                    'value': exclusive_start_yt_channel_key
                }
            )
        else:
            mirrorfm_cursors.delete_item(
                Key={
                    'name': 'exclusive_start_yt_channel_key'
                }
            )
            # TODO could re-try instead of stopping
            return
        channel_info = channel_info['Items'][0]
        channel_id = channel_info['channel_id']
        if 'last_upload_datetime' in channel_info:
            last_upload_datetime = get_datetime_from_iso8601_string(channel_info['last_upload_datetime'])
        if 'count_tracks' in channel_info:
            old_yt_count_tracks = channel_info['count_tracks']


    print(last_upload_datetime)
    print(upload_playlist_id)
    print(old_yt_count_tracks)

    # Disable OAuthlib's HTTPS verification when running locally.
    # *DO NOT* leave this option enabled in production.
    os.environ["OAUTHLIB_INSECURE_TRANSPORT"] = "1"
    youtube = discovery.build("youtube", "v3", developerKey=YT_DEVELOPER_KEY)

    try:
        response = youtube.channels().list(
            part="contentDetails,snippet",
            id=channel_id
        ).execute()
    except Exception as e:
        print(e)
        return

    try:
        upload_playlist_id = response['items'][0]['contentDetails']['relatedPlaylists']['uploads']
        channel_name = response['items'][0]['snippet']['title']
        thumbnails = response['items'][0]['snippet']['thumbnails']
    except IndexError as e:
        # Ignore malformatted event / channel_id
        print(e)
        return
    print(channel_name)

    mirrorfm_channels.update_item(
        Key={
            'host': 'yt',
            'channel_id': channel_id
        },
        UpdateExpression="set upload_playlist_id = :upload_playlist_id, thumbnails = :thumbnails, channel_name = :channel_name",
        ExpressionAttributeValues={
            ':channel_name': channel_name,
            ':upload_playlist_id': upload_playlist_id,
            ':thumbnails': thumbnails
        }
    )

    if not last_upload_datetime:
        process_full_list = True
        last_upload_datetime = datetime.min.replace(tzinfo=timezone.utc)
    else:
        process_full_list = False

    next_last_upload_datetime = last_upload_datetime
    pageToken = ""
    new_items_desc = []

    if process_full_list:
        while True:
            try:
                response = youtube.playlistItems().list(
                    part="snippet,contentDetails",
                    playlistId=upload_playlist_id,
                    maxResults=50,
                    pageToken=pageToken
                ).execute()
            except Exception as e:
                print(e)
                return
            for item in response['items']:
                next_last_upload_datetime = add_to_list_if_new_upload(item, new_items_desc, next_last_upload_datetime, last_upload_datetime, process_full_list)
            if 'nextPageToken' in response:
                pageToken = response['nextPageToken']
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
                    pageToken=pageToken,
                    publishedAfter=datetime_to_zulu(last_upload_datetime)
                ).execute()
            except Exception as e:
                print(e)
                return
            for item in response['items']:
                if item['snippet']['type'] == 'upload':
                    next_last_upload_datetime = add_to_list_if_new_upload(item, new_items_desc, next_last_upload_datetime, last_upload_datetime, process_full_list)
            if 'nextPageToken' in response:
                pageToken = response['nextPageToken']
            else:
                break

    i = 0
    for items in chunks(new_items_desc, 25):
        batch = []
        for item in items:
            id = get_video_id(process_full_list, item['contentDetails'])
            put_request = { 'PutRequest': { 'Item': {
                'yt_channel_id': channel_id,
                'yt_track_composite': '-'.join([item['snippet']['publishedAt'], id]),
                'yt_track_id': id,
                'yt_track_name': str(item['snippet']['title']),
                'yt_published_at': item['snippet']['publishedAt']
            }}}
            batch.append(put_request)
        dynamodb.batch_write_item(RequestItems={
           'mirrorfm_yt_tracks': batch
        })
        i += 1
        print("Batch sent %d/%d" % (i * 25, int(len(new_items_desc) / 25 + 1) * 25))

    # Update channel row with last_upload_datetime
    if next_last_upload_datetime and next_last_upload_datetime != last_upload_datetime:
        print(next_last_upload_datetime)
        mirrorfm_channels.update_item(
            Key={
                'host': 'yt',
                'channel_id': channel_id
            },
            UpdateExpression="set last_upload_datetime = :last_upload_datetime, count_tracks = :count_tracks",
            ExpressionAttributeValues={
                ':last_upload_datetime': datetime_to_zulu(next_last_upload_datetime),
                ':count_tracks': old_yt_count_tracks + len(new_items_desc)
            }
        )
    else:
        print("No new tracks")


# Quick local tests
if __name__ == "__main__":
    # Check next item (CRON mode)
    handle({}, {})

    # Add new channel
#     handle({'Records': [{'eventID': '4fe2aab7e1e242ae10debd11ce811eb7', 'eventName': 'INSERT', 'eventVersion': '1.1', 'eventSource': 'aws:dynamodb', 'awsRegion': 'eu-west-1', 'dynamodb': {'ApproximateCreationDateTime': 1572478081.0, 'Keys': {'host': {'S': 'yt'}, 'channel_id': {'S': 'UCSMUq7nmVCRpM3ZXAOhBCMw'}}, 'NewImage': {'host': {'S': 'yt'}, 'channel_id': {'S': 'UCSMUq7nmVCRpM3ZXAOhBCMw'}}, 'SequenceNumber': '75150100000000017939913152', 'SizeBytes': 80, 'StreamViewType': 'NEW_AND_OLD_IMAGES'}, 'eventSourceARN': 'arn:aws:dynamodb:eu-west-1:705440408593:table/mirrorfm_channels/stream/2019-10-16T21:39:59.018'}]}, {})