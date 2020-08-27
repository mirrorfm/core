"""
Search track names on Spotify
"""

import os
import sys

# https://github.com/apex/apex/issues/639#issuecomment-455883587
file_path = os.path.dirname(__file__)
module_path = os.path.join(file_path, "env")
sys.path.append(module_path)

from trackfilter.cli import split_artist_track
import spotipy.oauth2 as oauth2
import spotipy
from datetime import datetime, timezone
from pprint import pprint
import requests
import base64
import time
import decimal
import json
from boto3.dynamodb.types import TypeDeserializer
from boto3.dynamodb.conditions import Key
import boto3


deser = TypeDeserializer()


# custom exceptions
class SpotifyAPILimitReached(Exception):
    pass


# Helper class to convert a DynamoDB item to JSON.
class DecimalEncoder(json.JSONEncoder):
    def default(self, o):  # pylint: disable=E0202
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


PLAYLIST_EXPECTED_MAX_LENGTH = 11000
WEBSITE = "https://mirror.fm"
HOST = "yt"
BATCH_GET_SIZE = 1000

# DB
client = boto3.client("dynamodb", region_name='eu-west-1')
dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
cursors_table = dynamodb.Table('mirrorfm_cursors')
playlists_table = dynamodb.Table('mirrorfm_yt_playlists')
mirrorfm_channels = dynamodb.Table('mirrorfm_channels')
tracks_table = dynamodb.Table('mirrorfm_yt_tracks')
duplicates_table = dynamodb.Table('mirrorfm_yt_duplicates')
events_table = dynamodb.Table('mirrorfm_events')

# Spotify
SPOTIPY_CLIENT_ID = os.getenv('SPOTIPY_CLIENT_ID')
SPOTIPY_CLIENT_SECRET = os.getenv('SPOTIPY_CLIENT_SECRET')
SPOTIPY_USER = os.getenv('SPOTIPY_USER')
SPOTIPY_REDIRECT_URI = 'http://localhost/'

scope = 'playlist-read-private playlist-modify-private playlist-modify-public ugc-image-upload'


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
    with open("/tmp/.cache-" + SPOTIPY_USER, "w+") as f:
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
        cache_path='/tmp/.cache-' + SPOTIPY_USER
    )

    token_info = sp_oauth.get_cached_token()
    if not token_info:
        raise(Exception('null token_info'))
    store_spotify_token(token_info)

    return spotipy.Spotify(auth=token_info['access_token'])


def find_on_spotify(sp, track_name):
    artist_and_track = split_artist_track(track_name)
    if artist_and_track is not None and len(artist_and_track) > 1:
        query = 'track:"{0[1]}"+artist:"{0[0]}"'.format(artist_and_track)
    else:
        print("[?]", track_name)
        query = track_name
    try:
        results = sp.search(query, limit=1, type='track')
        for _, spotify_track in enumerate(results['tracks']['items']):
            return spotify_track
        # print("[x]", track_name, artist_and_track)
    except Exception as e:
        raise e


def get_last_playlist_for_channel(channel_id):
    res = playlists_table.query(
        ScanIndexForward=False,
        KeyConditionExpression=Key('yt_channel_id').eq(channel_id),
        Limit=1
    )
    if res['Count'] == 0:
        return None, None
    return [res['Items'][0], res['Items'][0]['num']]


def add_channel_cover_to_playlist(sp, channel_id, playlist_id):
    resp = mirrorfm_channels.get_item(
        Key={
            'host': 'yt',
            'channel_id': channel_id
        },
        AttributesToGet=[
            'thumbnails'
        ]
    )
    print(resp)
    if 'Item' in resp:
        image_url = resp['Item']['thumbnails']['medium']['url']
        sp.playlist_upload_cover_image(
            playlist_id, get_as_base64(image_url))


def get_as_base64(url):
    return base64.b64encode(requests.get(url).content).decode("utf-8")


def create_playlist_for_channel(sp, channel_id, num=1):
    res = mirrorfm_channels.query(
        ScanIndexForward=False,
        KeyConditionExpression=Key('host').eq(
            'yt') & Key('channel_id').eq(channel_id),
        Limit=1
    )
    playlist_name = res['Items'][0]['channel_name']
    if num > 1:
        playlist_name += ' (%d)' % num
    res = sp.user_playlist_create(SPOTIPY_USER, playlist_name, public=True)
    plid = res['id']
    item = {
        'yt_channel_id': channel_id,
        'num': num,
        'spotify_playlist': plid
    }
    playlists_table.put_item(
        Item=item
    )
    try:
        add_channel_cover_to_playlist(sp, channel_id, plid)
    except Exception as e:
        print(e)
    return [item, num]


def get_playlist_for_channel(sp, channel_id):
    pl, num = get_last_playlist_for_channel(channel_id)
    if pl:
        return pl, num
    return create_playlist_for_channel(sp, channel_id)


def is_track_duplicate(channel_id, track_spotify_uri):
    return 'Item' in duplicates_table.get_item(
        Key={
            'yt_channel_id': channel_id,
            'yt_track_id': track_spotify_uri
        }
    )


def add_track_to_duplicate_index(
        channel_id, track_spotify_uri, spotify_playlist):
    duplicates_table.put_item(
        Item={
            'yt_channel_id': channel_id,
            'yt_track_id': track_spotify_uri,
            'spotify_playlist': spotify_playlist
        }
    )


def playlist_seems_full(e, sp, spotify_playlist):
    if not (hasattr(e, 'http_status') and e.http_status in [403, 500]):
        return False
    # only query Spotify total as a last resort
    # https://github.com/spotify/web-api/issues/1179
    playlist = sp.user_playlist(SPOTIPY_USER, spotify_playlist, "tracks")
    total = playlist["tracks"]["total"]
    return total == PLAYLIST_EXPECTED_MAX_LENGTH


def add_track_to_spotify_playlist(sp, track_spotify_uri, channel_id):
    item, playlist_num = get_playlist_for_channel(sp, channel_id)
    spotify_playlist = item['spotify_playlist']
    try:
        sp.user_playlist_add_tracks(SPOTIPY_USER,
                                    spotify_playlist,
                                    [track_spotify_uri],
                                    position=0)
    except Exception as e:
        if playlist_seems_full(e, sp, spotify_playlist):
            spotify_playlist, _ = create_playlist_for_channel(sp, channel_id, playlist_num+1)
            # retry same function to use API limit logic
            add_track_to_spotify_playlist(sp, track_spotify_uri, channel_id)
        else:
            # Reached API limit?
            raise e
    add_track_to_duplicate_index(
        channel_id,
        track_spotify_uri,
        spotify_playlist)
    return spotify_playlist


def count_frequency(items):
    freq = {}
    for item in items:
        if item in freq:
            freq[item] += 1
        else:
            freq[item] = 1
    return freq


def find_genres(sp, info):
    global NEW_TRACKS_GENRES
    album = sp.album(info['album']['id'])
    song_genres = album['genres']

    for artist in info['artists']:
        info = sp.artist(artist['id'])
        song_genres = song_genres + info['genres']

    NEW_TRACKS_GENRES += song_genres
    return song_genres


def merge_genres(old_tracks_genres, new_tracks_genres):
    from collections import Counter
    A = Counter(old_tracks_genres)
    B = Counter(new_tracks_genres)
    return dict(A + B)


def spotify_lookup(sp, record):
    spotify_track_info = find_on_spotify(sp, record['yt_track_name'])

    # Safety duplicate check needed because
    # some duplicates were found in some playlists for unknown reasons.
    if spotify_track_info and not is_track_duplicate(
            record['yt_channel_id'], spotify_track_info['uri']):
        print(
            "[√]",
            spotify_track_info['uri'],
            spotify_track_info['artists'][0]['name'],
            "-",
            spotify_track_info['name'],
            "\n",
            "\t\t\t\t\t",
            record['yt_track_name'])
        genres = find_genres(sp, spotify_track_info)
        spotify_playlist = add_track_to_spotify_playlist(
            sp, spotify_track_info['uri'], record['yt_channel_id'])
        tracks_table.update_item(
            Key={
                'yt_channel_id': record['yt_channel_id'],
                'yt_track_composite': record['yt_track_composite']
            },
            UpdateExpression="set spotify_uri = :spotify_uri,\
                spotify_playlist = :spotify_playlist,\
                spotify_found_time = :spotify_found_time,\
                yt_track_name = :yt_track_name,\
                spotify_track_info = :spotify_track_info,\
                genres = :genres",
            ExpressionAttributeValues={
                ':spotify_uri': spotify_track_info['uri'],
                ':spotify_playlist': spotify_playlist,
                ':genres': genres,
                ':spotify_found_time': datetime.now(timezone.utc).isoformat(),
                ':yt_track_name': record['yt_track_name'],
                ':spotify_track_info': spotify_track_info
            }
        )
        return True


def get_current_or_next_channel():
    exclusive_start_yt_channel_track_key = get_cursor(
        'exclusive_start_yt_channel_track_key')
    if 'Item' in exclusive_start_yt_channel_track_key and exclusive_start_yt_channel_track_key['Item'] != {}:
        channel_to_process = mirrorfm_channels.query(
            Limit=1,
            ExclusiveStartKey=exclusive_start_yt_channel_track_key['Item']['value'],
            KeyConditionExpression=Key('host').eq('yt'))
    else:
        # no cursor, query first
        channel_to_process = mirrorfm_channels.query(
            Limit=1,
            KeyConditionExpression=Key('host').eq('yt'))

    if 'LastEvaluatedKey' not in channel_to_process:
        print("No next channel, re-initialize cursor")
        cursors_table.delete_item(
            Key={
                'name': 'exclusive_start_yt_channel_track_key'
            }
        )
        return get_current_or_next_channel()
    return channel_to_process


def save_cursors(just_processed_tracks, just_processed_channel):
    print('just_processed_channel', just_processed_channel)
    if 'LastEvaluatedKey' in just_processed_tracks:
        print('LastEvaluatedKey in just_processed_tracks')
        set_cursor('exclusive_start_yt_track_key',
                   just_processed_tracks['LastEvaluatedKey'])
        print('set cursor exclusive_start_yt_track_key with', just_processed_tracks['LastEvaluatedKey'])
    else:
        print('no LastEvaluatedKey in just_processed_tracks')
        cursors_table.delete_item(
            Key={
                'name': 'exclusive_start_yt_track_key'
            }
        )
        print('deleted exclusive_start_yt_track_key')
        if 'LastEvaluatedKey' in just_processed_channel:
            print('LastEvaluatedKey is in just_processed_channel')
            set_cursor(
                'exclusive_start_yt_channel_track_key',
                just_processed_channel['LastEvaluatedKey'])
            print('set cursor exclusive_start_yt_channel_track_key with', just_processed_channel['LastEvaluatedKey'])


def get_next_tracks(channel_id):
    exclusive_start_yt_track_key = get_cursor('exclusive_start_yt_track_key')
    if 'Item' in exclusive_start_yt_track_key:
        print(
            "Starting from track",
            exclusive_start_yt_track_key['Item']['value']['yt_track_composite'])
        print(channel_id)
        return tracks_table.query(
            Limit=BATCH_GET_SIZE,
            FilterExpression="attribute_not_exists(spotify_found_time)",
            ExclusiveStartKey=exclusive_start_yt_track_key['Item']['value'],
            KeyConditionExpression=Key('yt_channel_id').eq(channel_id))
    else:
        print("Starting from first track")
        return tracks_table.query(
            Limit=BATCH_GET_SIZE,
            FilterExpression="attribute_not_exists(spotify_found_time)",
            KeyConditionExpression=Key('yt_channel_id').eq(channel_id))


def deserialize_record(record):
    d = {}
    for key in record['NewImage']:
        d[key] = deser.deserialize(record['NewImage'][key])
    return d


def handle(event, context):
    global NEW_TRACKS_GENRES
    NEW_TRACKS_GENRES = []

    sp = get_spotify()
    total_added = total_searched = 0

    if 'Records' in event:
        # New tracks
        print("Process %d tracks just added to DynamoDB" %
              len(event['Records']))
        for record in event['Records']:
            record = record['dynamodb']
            if 'NewImage' in record and 'spotify_uri' not in record['NewImage']:
                total_searched += 1
                if spotify_lookup(sp, deserialize_record(record)):
                    total_added += 1
                channel_id = record['NewImage']['yt_channel_id']['S']
    else:
        # Rediscover tracks
        channel_to_process = get_current_or_next_channel()
        channel_name = None

        # Channel might not have a name yet if it has just been added
        if 'channel_name' in channel_to_process['Items'][0]:
            channel_name = channel_to_process['Items'][0]['channel_name']
        channel_id = channel_to_process['Items'][0]['channel_id']

        print("Rediscovering channel", channel_name or channel_id)

        tracks_to_process = get_next_tracks(channel_id)

        for record in tracks_to_process['Items']:
            if 'spotify_uri' not in record:
                total_searched += 1
                if spotify_lookup(sp, record):
                    total_added += 1
        save_cursors(tracks_to_process, channel_to_process)

    if total_searched > 0:
        print(
            "Searched %s, found %s track(s), updating channel info for %s" %
            (total_searched, total_added, channel_id))
        pl_item, num = get_last_playlist_for_channel(channel_id)
        if not pl_item:
            return

        pl_id = pl_item['spotify_playlist']
        pl = sp.playlist(pl_id)

        update_expr = "set count_followers = :count_followers, last_search_time = :last_search_time"
        expr_attrs = {
             ':count_followers': pl["followers"]["total"],
             ':last_search_time': int(time.time())
        }

        if total_added > 0:
            if 'genres' in pl_item:
                old_tracks_genres = pl_item['genres']
                playlist_genres = merge_genres(
                    old_tracks_genres,
                    count_frequency(NEW_TRACKS_GENRES))
            else:
                playlist_genres = count_frequency(NEW_TRACKS_GENRES)
            pprint(count_frequency(NEW_TRACKS_GENRES))

            update_expr += ", genres = :genres, last_found_time = :last_found_time, count_tracks = :count_tracks"
            expr_attrs[':last_found_time'] = int(time.time())
            expr_attrs[':genres'] = playlist_genres
            expr_attrs[':count_tracks'] = pl["tracks"]["total"]

            events_table.put_item(
                Item={
                    'host': 'yt',
                    'timestamp': int(time.time()),
                    'added': int(total_added),
                    'genres': count_frequency(NEW_TRACKS_GENRES),
                    'channel_id': channel_id,
                    'spotify_playlist': pl_id
                }
            )
        playlists_table.update_item(
            Key={
                'yt_channel_id': channel_id,
                'num': pl_item['num']
            },
            UpdateExpression=update_expr,
            ExpressionAttributeValues=expr_attrs
        )


if __name__ == "__main__":
    # Quick tests

    # Do nothing
    handle({}, {})

    # w/o Spotify URI -> add
    # handle({u'Records': [{u'eventID': u'7d3a0eeea532a920df49b37f63912dd7', u'eventVersion': u'1.1', u'dynamodb': {u'SequenceNumber': u'490449600000000013395897450', u'Keys': {u'yt_channel_id': {u'S': u'UCcHqeJgEjy3EJTyiXANSp6g'}, u'yt_track_id': {u'S': u'_fQ9DhnGo5Y'}}, u'SizeBytes': 103, u'NewImage': {u'yt_track_name': {u'S': u'eminem collapse'}, u'yt_channel_id': {u'S': u'UCcHqeJgEjy3EJTyiXANSp6g'}, u'yt_track_id': {u'S': u'_fQ9DhnGo5Y'}}, u'ApproximateCreationDateTime': 1558178610.0, u'StreamViewType': u'NEW_AND_OLD_IMAGES'}, u'awsRegion': u'eu-west-1', u'eventName': u'INSERT', u'eventSourceARN': u'arn:aws:dynamodb:eu-west-1:705440408593:table/any_tracks/stream/2019-05-06T10:02:12.102', u'eventSource': u'aws:dynamodb'}]}, {})

    # w/  Spotify URI -> don't add
    # handle({u'Records': [{u'eventID': u'7d3a0eeea532a920df49b37f63912dd7', u'eventVersion': u'1.1', u'dynamodb': {u'SequenceNumber': u'490449600000000013395897450', u'Keys': {u'yt_channel_id': {u'S': u'UCcHqeJgEjy3EJTyiXANSp6g'}, u'yt_track_id': {u'S': u'_fQ9DhnGo5Y'}}, u'SizeBytes': 103, u'NewImage': {u'yt_track_name': {u'S': u'eminem collapse'}, u'spotify_uri': {u'S': u'hi'}, u'yt_channel_id': {u'S': u'UCcHqeJgEjy3EJTyiXANSp6g'}, u'yt_track_id': {u'S': u'_fQ9DhnGo5Y'}}, u'ApproximateCreationDateTime': 1558178610.0, u'StreamViewType': u'NEW_AND_OLD_IMAGES'}, u'awsRegion': u'eu-west-1', u'eventName': u'INSERT', u'eventSourceARN': u'arn:aws:dynamodb:eu-west-1:705440408593:table/any_tracks/stream/2019-05-06T10:02:12.102', u'eventSource': u'aws:dynamodb'}]}, {})
