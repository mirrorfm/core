#!/usr/bin/python3.7

import boto3
import sys
import logging
from decimal import Decimal
import decimal
from datetime import datetime
import json

logger = logging.getLogger()
logger.setLevel(logging.INFO)

client = boto3.client("dynamodb", region_name='eu-west-1')
dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
mirrorfm_yt_playlists = dynamodb.Table('mirrorfm_yt_playlists')
mirrorfm_yt_channels = dynamodb.Table('mirrorfm_channels')


# RDS settings
import rds_config
import pymysql

host = rds_config.db_host
name = rds_config.db_username
password = rds_config.db_password
db_name = rds_config.db_name

try:
    conn = pymysql.connect(host, user=name, passwd=password, db=db_name, connect_timeout=5)
except pymysql.MySQLError as e:
    logger.error("ERROR: Unexpected error: Could not connect to MySQL instance.")
    logger.error(e)
    sys.exit()

item_count = 0


# Helper class to convert a DynamoDB item to JSON.
class DecimalEncoder(json.JSONEncoder):
    def default(self, o):
        if isinstance(o, decimal.Decimal):
            if o % 1 > 0:
                return float(o)
            else:
                return int(o)
        return super(DecimalEncoder, self).default(o)


# Get all
playlists = mirrorfm_yt_playlists.scan()
print(len(playlists['Items']))

channels = mirrorfm_yt_channels.scan()
print("channels:", len(channels['Items']))


def to_timestamp(unix):
    return datetime.utcfromtimestamp(int(unix)).strftime('%Y-%m-%d %H:%M:%S')


with conn.cursor() as cur:
    for p in channels['Items']:
        cur.execute('insert into yt_channels (channel_id, channel_name, count_tracks, last_upload_datetime, thumbnails, upload_playlist_id) values(%s, %s, %s, %s, %s, %s)',
                    [
                        p['channel_id'],
                        p['channel_name'],
                        str(Decimal(p['count_tracks'])),
                        p['last_upload_datetime'].replace("T", " ").replace(".000000Z", ""),
                        json.dumps(p['thumbnails'],
                                   ensure_ascii=False,
                                   cls=DecimalEncoder),
                        p['upload_playlist_id']
                    ])

    for p in playlists['Items']:
        cur.execute('insert into yt_playlists (channel_id, num, count_followers, count_tracks, last_found_time, last_search_time, spotify_playlist) values(%s, %s, %s, %s, %s, %s, %s)',
                    [
                        p['yt_channel_id'],
                        p['num'],
                        str(Decimal(p['count_followers'])),
                        str(Decimal(p['count_tracks'])),
                        to_timestamp(Decimal(p['last_found_time'])),
                        to_timestamp(Decimal(p['last_search_time'])),
                        p['spotify_playlist']
                    ])

    conn.commit()


