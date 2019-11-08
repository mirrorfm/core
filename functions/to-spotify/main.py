"""
Search track names on Spotify
"""

import os
import sys

# https://github.com/apex/apex/issues/639#issuecomment-455883587
file_path = os.path.dirname(__file__)
module_path = os.path.join(file_path, "env")
sys.path.append(module_path)

# # https://stackoverflow.com/a/39293287/1515819
# reload(sys)
# sys.setdefaultencoding('utf8')

import boto3
from boto3.dynamodb.conditions import Key, Attr
from botocore.exceptions import ClientError
from boto3.dynamodb.types import TypeDeserializer

deser = TypeDeserializer()

import json
import decimal
import time
import decimal
from pprint import pprint
from datetime import datetime, timezone

import spotipy
import spotipy.util as util
import spotipy.oauth2 as oauth2


# custom exceptions
class SpotifyAPILimitReached(Exception):
    pass


# Helper class to convert a DynamoDB item to JSON.
class DecimalEncoder(json.JSONEncoder):
    def default(self, o): # pylint: disable=E0202
        if isinstance(o, decimal.Decimal):
            if o % 1 > 0:
                return float(o)
            else:
                return int(o)
        return super(DecimalEncoder, self).default(o)


class Memoize:
    def __init__(self, f):
        self.f = f
        self.memo = {}

    def __call__(self, *args):
        if args not in self.memo:
            self.memo[args] = self.f(*args)

        return self.memo[args]


LAMBDA_EXEC_TIME = 110
PLAYLIST_EXPECTED_MAX_LENGTH = 11000
WEBSITE = "https://mirror.fm"
HOST = "yt"

# DB
client = boto3.client("dynamodb", region_name='eu-west-1')
dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
cursors_table = dynamodb.Table('mirrorfm_cursors')
playlists_table = dynamodb.Table('mirrorfm_yt_playlists')
mirrorfm_channels = dynamodb.Table('mirrorfm_channels')
tracks_table = dynamodb.Table('mirrorfm_yt_tracks')

# Spotify
SPOTIPY_CLIENT_ID = os.getenv('SPOTIPY_CLIENT_ID')
SPOTIPY_CLIENT_SECRET = os.getenv('SPOTIPY_CLIENT_SECRET')
SPOTIPY_USER = os.getenv('SPOTIPY_USER')
SPOTIPY_REDIRECT_URI = 'http://localhost/'

scope = 'playlist-read-private playlist-modify-private playlist-modify-public'


def get_cursor(name):
    return cursors_table.get_item(
        Key={
            'name': name
        },
        AttributesToGet=[
            'value'
        ]
    )


def set_cursor(name, position):
    cursors_table.put_item(
        Item={
            'name': name,
            'value': position
        }
    )

def restore_spotify_token():
    res = cursors_table.get_item(
        Key={
            'name': 'token'
        },
        AttributesToGet=[
            'value'
        ]
    )
    if 'Item' not in res:
        return 0

    token = res['Item']['value']
    with open("/tmp/.cache-"+SPOTIPY_USER, "w+") as f:
        f.write("%s" % json.dumps(token,
                                  ensure_ascii=False,
                                  cls=DecimalEncoder))

    # print("Restored token: %s" % token)


def store_spotify_token(token_info):
    cursors_table.put_item(
        Item={
            'name': 'token',
            'value': token_info
        }
    )
    # print("Stored token: %s" % token_info)


def get_spotify():
    restore_spotify_token()

    sp_oauth = oauth2.SpotifyOAuth(
            SPOTIPY_CLIENT_ID,
            SPOTIPY_CLIENT_SECRET,
            SPOTIPY_REDIRECT_URI,
            scope=scope,
            cache_path='/tmp/.cache-'+SPOTIPY_USER
        )

    token_info = sp_oauth.get_cached_token()
    if not token_info:
        raise(Exception('null token_info'))
    store_spotify_token(token_info)

    return spotipy.Spotify(auth=token_info['access_token'])


def find_on_spotify(sp, track_name):
    try:
        results = sp.search(track_name, limit=1, type='track')
        for _, spotify_track in enumerate(results['tracks']['items']):
            return spotify_track
    except Exception as e:
        raise e


def get_last_playlist_for_channel(channel_id):
    res = playlists_table.query(
        ScanIndexForward=False,
        KeyConditionExpression=Key('yt_channel_id').eq(channel_id),
        Limit=1
    )
    if res['Count'] == 0:
        return
    return [res['Items'][0]['spotify_playlist'], res['Items'][0]['num']]


def create_playlist_for_channel(sp, channel_id):
    num = 1
    res = mirrorfm_channels.query(
        ScanIndexForward=False,
        KeyConditionExpression=Key('host').eq('yt') & Key('channel_id').eq(channel_id),
        Limit=1
    )
    channel_name = res['Items'][0]['channel_name']
    res = sp.user_playlist_create(SPOTIPY_USER, channel_name, public=True)

    playlists_table.put_item(
        Item={
            'yt_channel_id': channel_id,
            'num': num,
            'spotify_playlist': res['id']
        }
    )
    return [res['id'], num]


def get_playlist_for_channel(sp, channel_id):
    return get_last_playlist_for_channel(channel_id) or \
           create_playlist_for_channel(sp, channel_id)


def add_track_to_spotify_playlist(sp, track_spotify_uri, channel_id):
    spotify_playlist, playlist_num = get_playlist_for_channel(sp, channel_id)
    sp.user_playlist_add_tracks(SPOTIPY_USER,
                                spotify_playlist,
                                [track_spotify_uri],
                                position=0)
    return spotify_playlist


def spotify_lookup(sp, record):
    spotify_track_info = find_on_spotify(sp, record['yt_track_name'])

    if spotify_track_info:
        print(spotify_track_info)
        spotify_playlist = add_track_to_spotify_playlist(sp, spotify_track_info['uri'], record['yt_channel_id'])
        tracks_table.update_item(
            Key={
                'yt_channel_id': record['yt_channel_id'],
                'yt_track_composite': record['yt_track_composite']
            },
            UpdateExpression="set spotify_uri = :spotify_uri,\
                spotify_playlist = :spotify_playlist,\
                spotify_found_time = :spotify_found_time,\
                yt_track_name = :yt_track_name,\
                spotify_track_info = :spotify_track_info",
            ExpressionAttributeValues={
                ':spotify_uri': spotify_track_info['uri'],
                ':spotify_playlist': spotify_playlist,
                ':spotify_found_time': datetime.now(timezone.utc).isoformat(),
                ':yt_track_name': record['yt_track_name'],
                ':spotify_track_info': spotify_track_info
            }
        )


def handle(event, context):
    print(event)
    sp = get_spotify()

    if 'Records' in event:
        for record in event['Records']:
            record = record['dynamodb']
            if 'NewImage' in record and 'spotify_uri' not in record['NewImage']:
                d = {}
                for key in record['NewImage']:
                    d[key] = deser.deserialize(record['NewImage'][key])
                spotify_lookup(sp, d)
    else:
        # rediscover channels
        exclusive_start_yt_channel_track_key = get_cursor('exclusive_start_yt_channel_track_key')
        print(exclusive_start_yt_channel_track_key)
        if 'Item' in exclusive_start_yt_channel_track_key and exclusive_start_yt_channel_track_key['Item'] != {}:
            channel_info = mirrorfm_channels.query(
                Limit=1,
                ExclusiveStartKey=exclusive_start_yt_channel_track_key['Item']['value'],
                KeyConditionExpression=Key('host').eq('yt'))
        else:
            # no cursor, query first
            channel_info = mirrorfm_channels.query(
                Limit=1,
                KeyConditionExpression=Key('host').eq('yt'))
        if len(channel_info['Items']) == 0:
            # First YT channel parsing didn't succeed/terminate?
            return
        print(channel_info)
        channel_id = channel_info['Items'][0]['channel_id']
        print(channel_id)

        # rediscover tracks
        exclusive_start_yt_track_key = get_cursor('exclusive_start_yt_track_key')
        print(exclusive_start_yt_track_key)

        if 'Item' in exclusive_start_yt_track_key:
            tracks = tracks_table.query(
                Limit=500,
                FilterExpression="attribute_not_exists(spotify_found_time)",
                ExclusiveStartKey=exclusive_start_yt_track_key['Item']['value'],
                KeyConditionExpression=Key('yt_channel_id').eq(channel_id))
        else:
            # no cursor, query first
            tracks = tracks_table.query(
                Limit=500,
                FilterExpression="attribute_not_exists(spotify_found_time)",
                KeyConditionExpression=Key('yt_channel_id').eq(channel_id))

        for record in tracks['Items']:
            spotify_lookup(sp, record)

        if 'LastEvaluatedKey' in tracks:
            set_cursor('exclusive_start_yt_track_key', tracks['LastEvaluatedKey'])
        else:
            cursors_table.delete_item(
                Key={
                    'name': 'exclusive_start_yt_track_key'
                }
            )
            if 'LastEvaluatedKey' in channel_info:
                set_cursor('exclusive_start_yt_channel_track_key', channel_info['LastEvaluatedKey'])
            else:
                cursors_table.delete_item(
                    Key={
                        'name': 'exclusive_start_yt_channel_track_key'
                    }
                )


if __name__ == "__main__":
    ### Quick tests

    # Do nothing
    handle({}, {})

    # w/o Spotify URI -> add
    # handle({u'Records': [{u'eventID': u'7d3a0eeea532a920df49b37f63912dd7', u'eventVersion': u'1.1', u'dynamodb': {u'SequenceNumber': u'490449600000000013395897450', u'Keys': {u'yt_channel_id': {u'S': u'UCcHqeJgEjy3EJTyiXANSp6g'}, u'yt_track_id': {u'S': u'_fQ9DhnGo5Y'}}, u'SizeBytes': 103, u'NewImage': {u'yt_track_name': {u'S': u'eminem collapse'}, u'yt_channel_id': {u'S': u'UCcHqeJgEjy3EJTyiXANSp6g'}, u'yt_track_id': {u'S': u'_fQ9DhnGo5Y'}}, u'ApproximateCreationDateTime': 1558178610.0, u'StreamViewType': u'NEW_AND_OLD_IMAGES'}, u'awsRegion': u'eu-west-1', u'eventName': u'INSERT', u'eventSourceARN': u'arn:aws:dynamodb:eu-west-1:705440408593:table/any_tracks/stream/2019-05-06T10:02:12.102', u'eventSource': u'aws:dynamodb'}]}, {})

    # w/  Spotify URI -> don't add
    # handle({u'Records': [{u'eventID': u'7d3a0eeea532a920df49b37f63912dd7', u'eventVersion': u'1.1', u'dynamodb': {u'SequenceNumber': u'490449600000000013395897450', u'Keys': {u'yt_channel_id': {u'S': u'UCcHqeJgEjy3EJTyiXANSp6g'}, u'yt_track_id': {u'S': u'_fQ9DhnGo5Y'}}, u'SizeBytes': 103, u'NewImage': {u'yt_track_name': {u'S': u'eminem collapse'}, u'spotify_uri': {u'S': u'hi'}, u'yt_channel_id': {u'S': u'UCcHqeJgEjy3EJTyiXANSp6g'}, u'yt_track_id': {u'S': u'_fQ9DhnGo5Y'}}, u'ApproximateCreationDateTime': 1558178610.0, u'StreamViewType': u'NEW_AND_OLD_IMAGES'}, u'awsRegion': u'eu-west-1', u'eventName': u'INSERT', u'eventSourceARN': u'arn:aws:dynamodb:eu-west-1:705440408593:table/any_tracks/stream/2019-05-06T10:02:12.102', u'eventSource': u'aws:dynamodb'}]}, {})