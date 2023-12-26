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


sp = spotipy.Spotify(auth_manager=spotipy.SpotifyOAuth(scope='playlist-read-private playlist-modify-public playlist-modify-private'))

ok = False
for pl in pls:
    channel_id = pl['channel_id']

    if not ok and channel_id == "UCqTwKvjbTENZDGbz2si47ag":
        ok = True
    if not ok:
        continue
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
    channel_track_ids = []
    for pl in playlists:
        offset = 0

        while True:
            res = sp.playlist_items(pl["spotify_playlist"], offset=offset)
            for item in res["items"]:
                if item['track'] is None:
                    continue
                channel_track_ids.append(item['track']["uri"])
            if len(res['items']) == 0:
                break
            offset = offset + len(res['items'])
            print(len(channel_track_ids))

    print(len(channel_track_ids), "items in pl")

    while True:
        res = mirrorfm_yt_tracks.query(**kwargs)

        for r in res['Items']:
            if 'spotify_uri' in r and r['spotify_uri'] not in channel_track_ids:
                print("should add", r['spotify_uri'], "in", playlists[-1]["spotify_playlist"])
                sp.playlist_add_items(playlists[-1]["spotify_playlist"], [r['spotify_uri']], position=0)
        if 'LastEvaluatedKey' in res:
            kwargs['ExclusiveStartKey'] = res["LastEvaluatedKey"]
        else:
            break
