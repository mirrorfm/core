"""
One-time backfill: recount count_tracks for all YouTube channels,
only counting titles that look like real tracks (Artist - Track pattern).

Usage:
    source functions/from-youtube/env.sh
    pip install trackfilter boto3 pymysql
    python utils/backfill-track-counts.py
"""

import os
import sys
import boto3
from boto3.dynamodb.conditions import Key
import pymysql
from trackfilter.cli import split_artist_track

db_host = os.getenv('DB_HOST')
db_user = os.getenv('DB_USERNAME')
db_pass = os.getenv('DB_PASSWORD')
db_name = os.getenv('DB_NAME')

conn = pymysql.connect(host=db_host, user=db_user, passwd=db_pass, db=db_name,
                       connect_timeout=10, cursorclass=pymysql.cursors.DictCursor)

dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
tracks_table = dynamodb.Table('mirrorfm_yt_tracks')

cursor = conn.cursor()
cursor.execute("SELECT channel_id, channel_name, count_tracks FROM yt_channels")
channels = cursor.fetchall()

print(f"Processing {len(channels)} channels...")

updated = 0
for ch in channels:
    channel_id = ch['channel_id']
    old_count = ch['count_tracks']

    # Scan all tracks for this channel
    track_count = 0
    response = tracks_table.query(
        KeyConditionExpression=Key('yt_channel_id').eq(channel_id),
        Select='SPECIFIC_ATTRIBUTES',
        ProjectionExpression='yt_track_name'
    )
    for item in response['Items']:
        if split_artist_track(item.get('yt_track_name', '')):
            track_count += 1

    while 'LastEvaluatedKey' in response:
        response = tracks_table.query(
            KeyConditionExpression=Key('yt_channel_id').eq(channel_id),
            Select='SPECIFIC_ATTRIBUTES',
            ProjectionExpression='yt_track_name',
            ExclusiveStartKey=response['LastEvaluatedKey']
        )
        for item in response['Items']:
            if split_artist_track(item.get('yt_track_name', '')):
                track_count += 1

    if track_count != old_count:
        cursor.execute("UPDATE yt_channels SET count_tracks = %s WHERE channel_id = %s",
                       [track_count, channel_id])
        print(f"  {ch['channel_name']}: {old_count} -> {track_count}")
        updated += 1

conn.commit()
conn.close()
print(f"Done. Updated {updated}/{len(channels)} channels.")
