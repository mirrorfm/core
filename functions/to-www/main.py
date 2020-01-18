#!/usr/bin/python3.7

import boto3
from pprint import pprint
from boto3.dynamodb.conditions import Key
import json
from pprint import pprint

client = boto3.client("dynamodb", region_name='eu-west-1')
dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
mirrorfm_channels = dynamodb.Table('mirrorfm_channels')
mirrorfm_yt_playlists = dynamodb.Table('mirrorfm_yt_playlists')


def get_table_count(table_name):
    return client.describe_table(TableName=table_name)["Table"]["ItemCount"]


def handle(event, context):
    arr = {
        "youtube": {
            "channels": [],
            "total_channels": get_table_count('mirrorfm_channels'),
            "total_tracks": get_table_count('mirrorfm_yt_tracks'),
            "found_tracks": get_table_count('mirrorfm_yt_duplicates')
        }
    }
    playlists = mirrorfm_yt_playlists.scan()
    playlists_map = {pl['yt_channel_id']: pl for pl in playlists['Items']}
    channels = mirrorfm_channels.query(KeyConditionExpression=Key('host').eq('yt'))["Items"]
    for c in channels:
        pl = playlists_map[c['channel_id']]
        c['found_tracks'] = pl.get('count_tracks')
        c['count_followers'] = pl.get('count_followers')
        c['spotify_playlist_id'] = pl.get('spotify_playlist')
        c['last_search_time'] = pl.get('last_search_time')
        c['genres'] = pl.get('genres')
    arr["youtube"]["channels"] = channels
    return arr


if __name__ == "__main__":
    pprint(handle({}, {}))