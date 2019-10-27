#!/usr/bin/python3.6

import os
import sys

# https://github.com/apex/apex/issues/639#issuecomment-455883587
file_path = os.path.dirname(__file__)
module_path = os.path.join(file_path, "env")
sys.path.append(module_path)

import traceback
from pprint import pprint
from googleapiclient import discovery
from datetime import datetime, timezone
import dateutil.parser
import boto3
from boto3.dynamodb.conditions import Key, Attr
from botocore.exceptions import ClientError

# DB
dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
mirrorfm_channels = dynamodb.Table('mirrorfm_channels')

scopes = ["https://www.googleapis.com/auth/youtube.readonly"]

YT_DEVELOPER_KEY = os.getenv('YT_DEVELOPER_KEY')


def get_datetime_from_iso8601_string(s):
    return dateutil.parser.parse(s)


def chunks(l, n):
    """Yield successive n-sized chunks from l."""
    for i in range(0, len(l), n):
        yield l[i:i + n]


def handle(event, context):
    print(event)
    if event['Records'][0]['eventName'] != "INSERT":
        # Only respond to new entries, not updates
        return event['Records'][0]['eventName']
    channel_id = event['Records'][0]['dynamodb']['Keys']['channel_id']['S']
    # Disable OAuthlib's HTTPS verification when running locally.
    # *DO NOT* leave this option enabled in production.
    os.environ["OAUTHLIB_INSECURE_TRANSPORT"] = "1"

    print(channel_id)
    youtube = discovery.build("youtube", "v3", developerKey=YT_DEVELOPER_KEY)

    channel_info = mirrorfm_channels.get_item(
        Key={
            'host': 'yt',
            'channel_id': channel_id
        },
        AttributesToGet=[
            'last_upload_datetime',
            'upload_playlist_id',
            'count_tracks'
        ]
    )
    upload_playlist_id = None
    last_upload_datetime = None
    old_yt_count_tracks = 0
    if 'Item' in channel_info:
        channel_info = channel_info['Item']
        if 'last_upload_datetime' in channel_info:
            last_upload_datetime = get_datetime_from_iso8601_string(channel_info['last_upload_datetime'])
        if 'upload_playlist_id' in channel_info:
            upload_playlist_id = channel_info['upload_playlist_id']
        if 'count_tracks' in channel_info:
            old_yt_count_tracks = channel_info['count_tracks']
    if not upload_playlist_id:
        response = youtube.channels().list(
            part="contentDetails,snippet",
            id=channel_id
        ).execute()

        upload_playlist_id = response['items'][0]['contentDetails']['relatedPlaylists']['uploads']
        channel_name = response['items'][0]['snippet']['title']

        mirrorfm_channels.put_item(
            Item={
                'host': 'yt',
                'channel_id': channel_id,
                'channel_name': channel_name,
                'upload_playlist_id': upload_playlist_id,
            }
        )
    if not last_upload_datetime:
        last_upload_datetime = datetime.min.replace(tzinfo=timezone.utc)

    next_last_upload_datetime = last_upload_datetime
    next_last_upload_datetime_str = ""
    pageToken = ""
    new_items_desc = []

    if True:
        while True:
            response = youtube.playlistItems().list(
                part="contentDetails,snippet",
                playlistId=upload_playlist_id,
                maxResults=50,
                pageToken=pageToken
            ).execute()
            for item in response['items']:
                item_datetime = get_datetime_from_iso8601_string(item['snippet']['publishedAt'])
                if item_datetime > last_upload_datetime:
                    print(item['id'] + " " + item['snippet']['publishedAt'] + " - " + str(item['snippet']['title']))
                    new_items_desc.append(item)
                    if item_datetime > next_last_upload_datetime:
                        next_last_upload_datetime = item_datetime
                        next_last_upload_datetime_str = item['snippet']['publishedAt']
            if 'nextPageToken' in response:
                pageToken = response['nextPageToken']
            else:
                break
        # YT API does not allow ASC
        # https://stackoverflow.com/a/22898075/1515819
        new_items_desc.reverse()
    else:
        # TODO-1: use getActivities to only get newly added tracks
        # https://stackoverflow.com/a/23286845/1515819
        pass

    print(next_last_upload_datetime_str)

    for items in chunks(new_items_desc, 25):
        dynamodb.batch_write_item(RequestItems={
            'mirrorfm_yt_tracks': [{ 'PutRequest': { 'Item': {
                'yt_channel_id': channel_id,
                'yt_track_id': item['id'],
                'yt_track_name': str(item['snippet']['title']),
                'yt_published_at': item['snippet']['publishedAt']
            }}} for item in items]
        })
        print("Batch sent")


    # Update channel row with last_upload_datetime
    # will be used for TODO-1
    if next_last_upload_datetime_str:
        mirrorfm_channels.update_item(
            Key={
                'host': 'yt',
                'channel_id': channel_id
            },
            UpdateExpression="set last_upload_datetime = :last_upload_datetime, count_tracks = :count_tracks",
            ExpressionAttributeValues={
                ':last_upload_datetime': next_last_upload_datetime_str,
                ':count_tracks': old_yt_count_tracks + len(new_items_desc)
            }
        )


if __name__ == "__main__":
    handle({}, {})