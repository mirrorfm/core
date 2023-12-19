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
import requests
import base64
import time
import decimal
import json
from boto3.dynamodb.types import TypeDeserializer
from boto3.dynamodb.conditions import Key
import boto3
import pymysql
import random
import re
from difflib import SequenceMatcher

db_username = os.getenv('DB_USERNAME')
db_password = os.getenv('DB_PASSWORD')
db_name = os.getenv('DB_NAME')
db_host = os.getenv('DB_HOST')

deser = TypeDeserializer()


TRACK_SIMILARITY_THRESHOLD = 0.8
TRACK_SIMILARITY_EXCLUDES = [
    "radio version",
    "original mix",
    "original version",
    "club mix",
    "instrumental",
    "remix"
]


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

BATCH_GET_SIZE = 1000

# DB
client = boto3.client("dynamodb", region_name='eu-west-1')
dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
cursors_table = dynamodb.Table('mirrorfm_cursors')
events_table = dynamodb.Table('mirrorfm_events')

YT_HOST = "yt"
DG_HOST = "dg"

cats = {
    YT_HOST: {
        "key": YT_HOST,
        "tracks_table": dynamodb.Table('mirrorfm_yt_tracks'),
        "duplicates_table": dynamodb.Table('mirrorfm_yt_duplicates'),
        "entity_id": "channel_id",
        "host_entity_id": "yt_channel_id",
        "host_entity_id_type": str,
        "entity_name": "channel_name",
        "track_id": "yt_track_id",
        "track_name": "yt_track_name",
        "track_composite": "yt_track_composite",
        "entity_table": "yt_channels",
        "genres_table": "yt_genres",
        "playlist_table": "yt_playlists",
        "cursor_start_track_key": "start_yt_track_key",
        "cursor_last_successful_entity": "to_spotify_last_successful_channel",
        "description": "YouTube channel",
        "track_parsing_needed": True,
        "duplicate_spotify_id": "yt_track_id",
        "thumbnail_needs_resize": False
    },
    DG_HOST: {
        "key": DG_HOST,
        "tracks_table": dynamodb.Table('mirrorfm_dg_tracks'),
        "duplicates_table": dynamodb.Table('mirrorfm_dg_duplicates'),
        "entity_id": "label_id",
        "host_entity_id": "dg_label_id",
        "host_entity_id_type": int,
        "entity_name": "label_name",
        "track_id": "dg_track_id",
        "track_name": "dg_track_name",
        "track_composite": "dg_track_composite",
        "entity_table": "dg_labels",
        "genres_table": "dg_genres",
        "playlist_table": "dg_playlists",
        "cursor_start_track_key": "start_dg_track_key",
        "cursor_last_successful_entity": "to_spotify_last_successful_label",
        "description": "Discogs label",
        "track_parsing_needed": False,
        "duplicate_spotify_id": "spotify_uri",
        "thumbnail_needs_resize": True,
    }
}

# Spotify
SPOTIPY_CLIENT_ID = os.getenv('SPOTIPY_CLIENT_ID')
SPOTIPY_CLIENT_SECRET = os.getenv('SPOTIPY_CLIENT_SECRET')
SPOTIPY_REDIRECT_URI = 'http://localhost/'
SPOTIPY_USER = os.getenv('SPOTIPY_USER')

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
    with open("/tmp/.cache", "w+") as f:
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
        cache_path='/tmp/.cache'
    )

    token_info = sp_oauth.get_cached_token()
    if not token_info:
        raise (Exception('null token_info'))
    store_spotify_token(token_info)

    return spotipy.Spotify(auth=token_info['access_token'])


def find_youtube_track_on_spotify(handler, track_name):
    artist_and_track = split_artist_track(track_name)
    if artist_and_track is not None and len(artist_and_track) > 1:
        track = '{0[1]}'.format(artist_and_track).strip()
        artist = '{0[0][0]}'.format(artist_and_track).strip()
        query = 'track:{0} artist:{1}'.format(track, artist)
    else:
        print("[?]", track_name)
        query = track_name
    return find_track_on_spotify(handler, query)


def find_discogs_track_on_spotify(handler, track_name, artist):
    artist = cleanse_artist(artist)
    query = 'track:{0} artist:{1}'.format(track_name.strip(), artist.strip())
    return find_track_on_spotify(handler, query)


def cleanse_artist(artist):
    # https://regex101.com/r/H9m4pk/1
    # could be improved
    return re.sub(r"\(.*\)", "", artist).strip()


def find_track_on_spotify(handler, query):
    if len(query) > 100:
        print("Length was > 100", len(query), query)
        return
    try:
        results = handler.sp.search(query, limit=1, type='track')
    except Exception as e:
        print(e)
        if e.args[0] == 404:
            # TODO sometimes length > 100 with special characters
            return
        raise e
    for _, spotify_track in enumerate(results['tracks']['items']):
        return spotify_track


def get_last_playlist(handler, entity_id):
    cursor = handler.conn.cursor()
    cursor.execute('SELECT * FROM ' + cats[handler.current_host]['playlist_table'] + ' WHERE '
                   + cats[handler.current_host]['entity_id'] + '="%s" ORDER BY num DESC LIMIT 1' % entity_id)
    playlist = cursor.fetchone()
    if not playlist:
        return None, None
    return [playlist, playlist['num']]  # full item, num


def get_as_base64(url):
    return base64.b64encode(requests.get(url).content).decode("utf-8")


def resize_as_base64(url):
    from PIL import Image
    from io import BytesIO
    from urllib.request import urlopen

    im = Image.open(urlopen(url))
    new_image = im.resize((300, 300))
    buffered = BytesIO()
    new_image.save(buffered, format="JPEG")
    return base64.b64encode(buffered.getvalue())


def add_channel_cover_to_playlist(handler, entity_id, playlist_id):
    cursor = handler.conn.cursor()
    cursor.execute('SELECT * FROM ' + cats[handler.current_host]['entity_table'] + ' WHERE '
                   + cats[handler.current_host]['entity_id'] + '="%s"' % entity_id)
    row = cursor.fetchone()
    if row:
        thumbnail = row['thumbnail_medium']
        b64 = (resize_as_base64(thumbnail) if cats[handler.current_host]['thumbnail_needs_resize']
               else get_as_base64(thumbnail))
        return handler.sp.playlist_upload_cover_image(playlist_id, b64)


def create_playlist(handler, entity_id, num=1):
    cursor = handler.conn.cursor()
    cursor.execute("SELECT * FROM " + cats[handler.current_host]['entity_table'] + " WHERE "
                   + cats[handler.current_host]['entity_id'] + "='%s'" % entity_id)
    row = cursor.fetchone()

    playlist_name = row[cats[handler.current_host]['entity_name']]
    if num > 1:
        playlist_name += ' (%d)' % num
    res = handler.sp.user_playlist_create(SPOTIPY_USER, playlist_name, public=True)
    playlist_id = res['id']
    item = {
        cats[handler.current_host]['host_entity_id']: entity_id,
        'num': num,
        'spotify_playlist': playlist_id
    }
    cur = handler.conn.cursor()
    cur.execute('insert into ' + cats[handler.current_host]['playlist_table']
                + ' (' + cats[handler.current_host]['entity_id'] + ', num, spotify_playlist) values(%s, %s, %s)',
                [entity_id, num, playlist_id])
    handler.conn.commit()
    try:
        add_channel_cover_to_playlist(handler, entity_id, playlist_id)
        add_channel_cover_to_playlist(handler, entity_id, playlist_id)
    except Exception as e:
        print(e)
    return [item, num]


def get_playlist(handler, entity_id):
    pl, num = get_last_playlist(handler, entity_id)
    if pl:
        return pl, num
    return create_playlist(handler, entity_id)


def is_track_duplicate(handler, entity_id, track_spotify_uri):
    table = cats[handler.current_host]['duplicates_table']
    return 'Item' in table.get_item(
        Key={
            cats[handler.current_host]['host_entity_id']: entity_id,
            cats[handler.current_host]['duplicate_spotify_id']: track_spotify_uri
        }
    )


def add_track_to_duplicate_index(handler, entity_id, track_spotify_uri, spotify_playlist):
    table = cats[handler.current_host]['duplicates_table']
    table.put_item(
        Item={
            cats[handler.current_host]['host_entity_id']: entity_id,
            cats[handler.current_host]['duplicate_spotify_id']: track_spotify_uri,
            'spotify_playlist': spotify_playlist
        }
    )


def playlist_seems_full(e, handler, spotify_playlist):
    if hasattr(e, 'http_status') and e.http_status in [403, 500]:
        return False
    # only query Spotify total as a last resort
    # https://github.com/spotify/web-api/issues/1179
    playlist = handler.sp.user_playlist(SPOTIPY_USER, spotify_playlist, "tracks")
    total = playlist["tracks"]["total"]
    return total == PLAYLIST_EXPECTED_MAX_LENGTH


def add_track_to_spotify_playlist(handler, track_spotify_uri, entity_id):
    item, playlist_num = get_playlist(handler, entity_id)
    spotify_playlist = item['spotify_playlist']
    try:
        handler.sp.user_playlist_add_tracks(SPOTIPY_USER,
                                            spotify_playlist,
                                            [track_spotify_uri],
                                            position=0)
    except Exception as e:
        if playlist_seems_full(e, handler, spotify_playlist):
            spotify_playlist, _ = create_playlist(handler, entity_id, playlist_num + 1)
            # retry same function to use API limit logic
            add_track_to_spotify_playlist(handler, track_spotify_uri, entity_id)
        else:
            # Reached API limit?
            raise e
    add_track_to_duplicate_index(handler,
                                 entity_id,
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


def find_genres(handler, info, new_track_genres):
    album = handler.sp.album(info['album']['id'])
    song_genres = album['genres']

    for artist in info['artists']:
        info = handler.sp.artist(artist['id'])
        song_genres = song_genres + info['genres']

    new_track_genres += song_genres
    return song_genres


def get_first_artist(record):
    if "artistssort" in record:
        if record["artistssort"] == "Various":
            return record["artists"][0]["name"]
        return record["artistssort"]
    if "artists" in record:
        return record["artists"][0]["name"]
    if "release_artistssort" in record and record["release_artistssort"] == "Various":
        return record["release_artists"][0]["name"]
    return record["release_artistssort"]


def similar(a, b):
    return SequenceMatcher(None, a, b).ratio()


def spotify_lookup(handler, record, new_track_genres):
    if cats[handler.current_host]['track_parsing_needed']:
        track_name = record[cats[handler.current_host]['track_name']]
        spotify_track_info = find_youtube_track_on_spotify(handler, track_name)
    else:
        if 'title' in record and record['title'] is None or 'title' not in record:
            # Discogs can have None title
            return False
        artist = get_first_artist(record)
        # TODO remove number in `Artist (number)`
        spotify_track_info = find_discogs_track_on_spotify(handler, record['title'], artist)
        track_name = artist + " - " + record['title']
    tracks_table = cats[handler.current_host]['tracks_table']

    # Safety duplicate check needed because
    # some duplicates were found in some playlists for unknown reasons.
    if spotify_track_info and not is_track_duplicate(handler,
                                                     record[cats[handler.current_host]['host_entity_id']],
                                                     spotify_track_info['uri']):
        found_track = " - ".join([spotify_track_info['artists'][0]['name'], spotify_track_info['name']])

        def sanitize(track):
            # make track alphanumeric and lowercase
            # also removes frequent track suffixes such as `Original Mix`
            track = ''.join(ch for ch in track if ch.isalnum()).lower()
            for sub in TRACK_SIMILARITY_EXCLUDES:
                track = track.replace(sub, '')
            return track

        similarity = similar(sanitize(track_name), sanitize(found_track))

        if similarity < TRACK_SIMILARITY_THRESHOLD:
            return

        print(
            "[√]",
            spotify_track_info['uri'],
            spotify_track_info['artists'][0]['name'],
            "-",
            spotify_track_info['name'],
            "\n\t\t\t\t\t",
            track_name,
            "\n\t\t\t\t\t",
            similarity)
        genres = find_genres(handler, spotify_track_info, new_track_genres)
        spotify_playlist = add_track_to_spotify_playlist(
            handler, spotify_track_info['uri'], record[cats[handler.current_host]['host_entity_id']])
        tracks_table.update_item(
            Key={
                cats[handler.current_host]['host_entity_id']: record[cats[handler.current_host]['host_entity_id']],
                cats[handler.current_host]['track_composite']: record[cats[handler.current_host]['track_composite']]
            },
            UpdateExpression="set spotify_uri = :spotify_uri,\
                spotify_playlist = :spotify_playlist,\
                spotify_found_time = :spotify_found_time,\
                %s = :%s,\
                spotify_track_info = :spotify_track_info,\
                genres = :genres" % (cats[handler.current_host]['track_name'],
                                     cats[handler.current_host]['track_name']),
            ExpressionAttributeValues={
                ':spotify_uri': spotify_track_info['uri'],
                ':spotify_playlist': spotify_playlist,
                ':genres': genres,
                ':spotify_found_time': datetime.now(timezone.utc).isoformat(),
                ':%s' % cats[handler.current_host]['track_name']: track_name,
                ':spotify_track_info': spotify_track_info
            }
        )
        return True


def get_next_entity(handler):
    cursor = get_cursor(cats[handler.current_host]['cursor_last_successful_entity'])
    if 'Item' in cursor and cursor['Item']['value']:
        last_entity_id = int(cursor['Item']['value'])
    else:
        last_entity_id = 0
    cursor = handler.conn.cursor()
    cursor.execute("SELECT * FROM " + cats[handler.current_host]['entity_table']
                   + " WHERE (id > %s or id = 1) order by id = 1, id limit 1" % str(last_entity_id))
    return cursor.fetchone()


def save_cursors(handler, just_processed_tracks, to_spotify_last_successful_entity):
    if 'LastEvaluatedKey' in just_processed_tracks:
        print('LastEvaluatedKey in just_processed_tracks')
        set_cursor(cats[handler.current_host]['cursor_start_track_key'],
                   just_processed_tracks['LastEvaluatedKey'])
        print('set cursor %s with' % cats[handler.current_host]['cursor_start_track_key'],
              just_processed_tracks['LastEvaluatedKey'])
    else:
        print('no LastEvaluatedKey in just_processed_tracks')
        cursors_table.delete_item(
            Key={
                'name': cats[handler.current_host]['cursor_start_track_key']
            }
        )
        print('deleted %s' % cats[handler.current_host]['cursor_start_track_key'])
        set_cursor(cats[handler.current_host]['cursor_last_successful_entity'], to_spotify_last_successful_entity)
        print('set cursor %s with' % cats[handler.current_host]['cursor_last_successful_entity'],
              to_spotify_last_successful_entity)


def get_next_tracks(handler, entity_id):
    tracks_table = cats[handler.current_host]['tracks_table']
    cursor = get_cursor(cats[handler.current_host]['cursor_start_track_key'])
    host_entity_id = cats[handler.current_host]['host_entity_id']
    host_entity_id_type = cats[handler.current_host]['host_entity_id_type']

    if host_entity_id_type == int:
        entity_id = int(entity_id)

    condition_expression = Key(host_entity_id).eq(entity_id)

    if 'Item' in cursor and entity_id == cursor['Item']['value'][host_entity_id]:
        print(
            "Starting from track",
            cursor['Item']['value'][cats[handler.current_host]['track_composite']])

        return tracks_table.query(
            Limit=BATCH_GET_SIZE,
            FilterExpression="attribute_not_exists(spotify_found_time)",
            ExclusiveStartKey=cursor['Item']['value'],
            KeyConditionExpression=condition_expression)
    else:
        print("Starting from first track")
        return tracks_table.query(
            Limit=BATCH_GET_SIZE,
            FilterExpression="attribute_not_exists(spotify_found_time)",
            KeyConditionExpression=condition_expression)


def deserialize_record(record):
    d = {}
    for key in record['NewImage']:
        d[key] = deser.deserialize(record['NewImage'][key])
    return d


def update_playlist_description(handler, pl_id, channel_aid):
    cursor = handler.conn.cursor()
    cursor.execute("SELECT * FROM " + cats[handler.current_host]['genres_table']
                   + " WHERE " + cats[handler.current_host]['host_entity_id']
                   + "=%s ORDER BY count DESC LIMIT 6" % channel_aid)
    genres = cursor.fetchall()
    genre_names = [g["genre_name"] for g in genres]

    # Description
    genres_str = ''
    if len(genre_names) > 0:
        genres_str = ' with ' + ', '.join(genre_names)
    desc = cats[handler.current_host]['description'] + genres_str + \
           ". Add any youtube channel or discogs label on www.mirror.fm #mirrorfm"
    handler.sp.playlist_change_details(pl_id, description=desc)


class HostNotFound(Exception):
    pass


def detect_host(event):
    for record in event['Records']:
        record = record['dynamodb']
        if 'Keys' in record:
            keys = record['Keys']
            for k in cats:
                cat = cats[k]
                if cat['host_entity_id'] in keys:
                    cat_key = cat['key']
                    print("detected host %s" % cat_key)
                    return cat_key
    raise HostNotFound


def random_host():
    rand_boolean = bool(random.getrandbits(1))
    rand_host = YT_HOST if rand_boolean else DG_HOST
    print("random host %s" % rand_host)
    return rand_host


class Handler(object):
    sp = None
    current_host = None
    conn = None


def handle(event, c):
    handler = Handler()
    handler.sp = get_spotify()

    try:
        handler.conn = pymysql.connect(db_host,
                                       user=db_username,
                                       passwd=db_password,
                                       db=db_name,
                                       connect_timeout=5,
                                       cursorclass=pymysql.cursors.DictCursor)
    except pymysql.MySQLError as e:
        print("ERROR: Unexpected error: Could not connect to MySQL instance.")
        print(e)
        sys.exit()

    new_track_genres = []

    total_added = total_searched = 0

    if 'Records' in event and len(event['Records']) > 0:
        handler.current_host = detect_host(event)

        # New tracks
        print("Process %d tracks just added to DynamoDB" % len(event['Records']))
        for record in event['Records']:
            record = record['dynamodb']
            if 'NewImage' in record and 'spotify_uri' not in record['NewImage']:
                total_searched += 1
                if spotify_lookup(handler, deserialize_record(record), new_track_genres):
                    total_added += 1
        if cats[handler.current_host]['host_entity_id_type'] == str:
            entity_id_type = "S"
        else:
            entity_id_type = "N"
        entity_id = event['Records'][0]['dynamodb']['NewImage'][cats[handler.current_host]['host_entity_id']][
            entity_id_type]
        cursor = handler.conn.cursor()
        cursor.execute(
            "SELECT * FROM " + cats[handler.current_host]['entity_table'] + " WHERE " + cats[handler.current_host][
                'entity_id'] + "=%s", entity_id)
        entity = cursor.fetchone()
        entity_aid = entity['id']
        entity_name = entity[cats[handler.current_host]['entity_name']]
    else:
        handler.current_host = random_host()

        # Rediscover tracks
        channel_to_process = get_next_entity(handler)
        entity_aid = channel_to_process['id']
        entity_id = channel_to_process[cats[handler.current_host]['entity_id']]

        # Channel might not have a name yet if it has just been added
        entity_name = channel_to_process[cats[handler.current_host]['entity_name']]
        print("Rediscovering entity", entity_name or entity_id)

        tracks_to_process = get_next_tracks(handler, entity_id)

        for record in tracks_to_process['Items']:
            if 'spotify_uri' not in record:
                total_searched += 1
                if spotify_lookup(handler, record, new_track_genres):
                    total_added += 1

        save_cursors(handler, tracks_to_process, entity_aid)

    if total_searched > 0:
        print(
            "Searched %s, found %s track(s), updating entity info for %s" %
            (total_searched, total_added, entity_id))

        # TODO What if the code above updated 2 playlists?
        pl_item, num = get_last_playlist(handler, entity_id)
        if not pl_item:
            return

        pl_id = pl_item['spotify_playlist']
        update_playlist_description(handler, pl_id, entity_aid)
        pl = handler.sp.playlist(pl_id)

        cursor = handler.conn.cursor()

        if total_added > 0:
            playlist_genres = count_frequency(new_track_genres)
            events_table.put_item(
                Item={
                    'host': handler.current_host,
                    'timestamp': int(time.time()),
                    'added': int(total_added),
                    'genres': playlist_genres,
                    'entity_id': entity_id,
                    'spotify_playlist': pl_id,
                    'entity_name': entity_name
                }
            )
            cursor.execute('UPDATE ' + cats[handler.current_host]['playlist_table']
                           + ' SET count_followers=%s, last_search_time=now(), found_tracks=%s, last_found_time=now() WHERE spotify_playlist=%s AND num=%s',
                           [pl["followers"]["total"], pl["tracks"]["total"], pl_id, num])
            for genre in playlist_genres:
                cursor.execute(
                    'INSERT INTO ' + cats[handler.current_host]['genres_table']
                    + ' (' + cats[handler.current_host]['host_entity_id']
                    + ', genre_name, count, last_updated) VALUES ("%s", %s, 1, NOW()) ON DUPLICATE KEY UPDATE count = count + 1, last_updated=NOW()',
                    [entity_aid, genre])
        else:
            cursor.execute('UPDATE ' + cats[handler.current_host]['playlist_table']
                           + ' SET count_followers=%s, last_search_time=now() WHERE spotify_playlist=%s AND num=%s',
                           [pl["followers"]["total"], pl_id, num])

        handler.conn.commit()
        handler.conn.close()


if __name__ == "__main__":
    # Quick tests

    # Do nothing
    handle({}, {})

    # w/o Spotify URI -> add
    # handle({
    #     u'Records': [
    #         {
    #             u'eventID': u'7d3a0eeea532a920df49b37f63912dd7',
    #             u'eventVersion': u'1.1',
    #             u'dynamodb': {
    #                 u'SequenceNumber': u'490449600000000013395897450',
    #                 u'Keys': {
    #                     u'yt_channel_id': {u'S': u'UCcHqeJgEjy3EJTyiXANSp6g'},
    #                     u'yt_track_id': {u'S': u'_fQ9DhnGo5Y'}
    #                 },
    #                 u'SizeBytes': 103,
    #                 u'NewImage': {
    #                     u'yt_track_name': {u'S': u'eminem collapse'},
    #                     u'yt_channel_id': {u'S': u'UCcHqeJgEjy3EJTyiXANSp6g'},
    #                     u'yt_track_id': {u'S': u'_fQ9DhnGo5Y'}
    #                 },
    #                 u'ApproximateCreationDateTime': 1558178610.0,
    #                 u'StreamViewType': u'NEW_AND_OLD_IMAGES'
    #             },
    #             u'awsRegion': u'eu-west-1',
    #             u'eventName': u'INSERT',
    #             u'eventSourceARN': u'arn:aws:dynamodb:eu-west-1::table/any_tracks/stream/2019-05-06T10:02:12.102',
    #             u'eventSource': u'aws:dynamodb'
    #         }
    #     ]
    # }, {})

    # w/  Spotify URI -> don't add
    # handle({u'Records': [{u'eventID': u'7d3a0eeea532a920df49b37f63912dd7', u'eventVersion': u'1.1', u'dynamodb': {u'SequenceNumber': u'490449600000000013395897450', u'Keys': {u'yt_channel_id': {u'S': u'UCcHqeJgEjy3EJTyiXANSp6g'}, u'yt_track_id': {u'S': u'_fQ9DhnGo5Y'}}, u'SizeBytes': 103, u'NewImage': {u'yt_track_name': {u'S': u'eminem collapse'}, u'spotify_uri': {u'S': u'hi'}, u'yt_channel_id': {u'S': u'UCcHqeJgEjy3EJTyiXANSp6g'}, u'yt_track_id': {u'S': u'_fQ9DhnGo5Y'}}, u'ApproximateCreationDateTime': 1558178610.0, u'StreamViewType': u'NEW_AND_OLD_IMAGES'}, u'awsRegion': u'eu-west-1', u'eventName': u'INSERT', u'eventSourceARN': u'arn:aws:dynamodb:eu-west-1::table/any_tracks/stream/2019-05-06T10:02:12.102', u'eventSource': u'aws:dynamodb'}]}, {})
