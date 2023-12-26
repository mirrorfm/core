import boto3
import sys
import logging
import os
import pymysql
from pprint import pprint
from boto3.dynamodb.conditions import Key
import spotipy

logger = logging.getLogger()
logger.setLevel(logging.INFO)

host = os.getenv('DB_HOST')
name = os.getenv('DB_USERNAME')
password = os.getenv('DB_PASSWORD')
db_name = os.getenv('DB_NAME')

try:
    conn = pymysql.connect(host,
                           user=name,
                           passwd=password,
                           db=db_name,
                           connect_timeout=5,
                           cursorclass=pymysql.cursors.DictCursor)
except pymysql.MySQLError as e:
    logger.error("ERROR: Unexpected error: Could not connect to MySQL instance.")
    logger.error(e)
    sys.exit()

cursor = conn.cursor()
cursor.execute("SELECT * FROM yt_channels")
pls = cursor.fetchall()

dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
mirrorfm_yt_tracks = dynamodb.Table('mirrorfm_yt_tracks')
mirrorfm_yt_duplicates = dynamodb.Table('mirrorfm_yt_duplicates')


sp = spotipy.Spotify(auth_manager=spotipy.SpotifyOAuth(scope='playlist-modify-public playlist-modify-private'))


def remove(longer, shorter, remove_spotify_id, spotify_playlists, yt_channel_id):
    if remove_spotify_id:
        for pl in spotify_playlists:
            resp = sp.playlist_remove_all_occurrences_of_items(pl, [remove_spotify_id])
            pprint(resp)
            print("Removed from spotify playlist", pl, "id", remove_spotify_id)
        resp = mirrorfm_yt_duplicates.delete_item(Key={
            'yt_channel_id': yt_channel_id,
            'yt_track_id': remove_spotify_id
        })
        pprint(resp)
        print("Remove from mirrorfm_yt_duplicates", remove_spotify_id)
    resp = mirrorfm_yt_tracks.delete_item(Key={
        'yt_channel_id': yt_channel_id,
        'yt_track_composite': longer
    })
    pprint(resp)
    print("Remove from mirrorfm_yt_tracks", longer, "because shorter exists:", shorter)


for pl in pls:
    channel_id = pl['channel_id']
    print(channel_id, pl['channel_name'])
    condition_expression = Key('yt_channel_id').eq(channel_id)
    kwargs = {
        'KeyConditionExpression': condition_expression,
        'ScanIndexForward': True
    }

    try:
        conn = pymysql.connect(host,
                               user=name,
                               passwd=password,
                               db=db_name,
                               connect_timeout=5,
                               cursorclass=pymysql.cursors.DictCursor)
    except pymysql.MySQLError as e:
        logger.error("ERROR: Unexpected error: Could not connect to MySQL instance.")
        logger.error(e)
        sys.exit()

    cursor = conn.cursor()
    cursor.execute('SELECT * FROM yt_playlists WHERE channel_id="%s"' % channel_id)
    playlists = cursor.fetchall()
    spotify_playlists = [pl["spotify_playlist"] for pl in playlists]

    kv = {}
    total = 0
    removed = 0
    removed_spotify = 0

    while True:
        res = mirrorfm_yt_tracks.query(**kwargs)

        for r in res['Items']:
            total += 1

            new = r['yt_track_composite']
            if r['yt_track_name'] not in kv:
                kv[r['yt_track_name']] = new
            else:
                removed += 1
                if 'spotify_uri' in r:
                    remove_spotify_id = r['spotify_uri']
                    removed_spotify += 1
                else:
                    remove_spotify_id = None
                old = kv[r['yt_track_name']]
                if len(new) >= len(old):
                    remove(new, old, remove_spotify_id, spotify_playlists, channel_id)
                else:
                    remove(old, new, remove_spotify_id, spotify_playlists, channel_id)
                    kv[r['yt_track_name']] = new

            print("total", total, "removed", removed, "removed from spotify", removed_spotify)

        if 'LastEvaluatedKey' in res:
            kwargs['ExclusiveStartKey'] = res["LastEvaluatedKey"]
        else:
            break
