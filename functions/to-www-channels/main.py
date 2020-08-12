#!/usr/bin/python3.7

import os
import sys

# https://github.com/apex/apex/issues/639#issuecomment-455883587
file_path = os.path.dirname(__file__)
module_path = os.path.join(file_path, "env")
sys.path.append(module_path)

import boto3
from boto3.dynamodb.conditions import Key
from pprint import pprint
import simplejson as json

client = boto3.client("dynamodb", region_name='eu-west-1')
dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
mirrorfm_channels = dynamodb.Table('mirrorfm_channels')
mirrorfm_yt_playlists = dynamodb.Table('mirrorfm_yt_playlists')

def get_table_count(table_name):
    return client.describe_table(TableName=table_name)["Table"]["ItemCount"]


def populate(c, pl):
    c['found_tracks'] = pl.get('count_tracks')
    c['count_followers'] = pl.get('count_followers')
    c['spotify_playlist_id'] = pl.get('spotify_playlist')
    c['last_search_time'] = pl.get('last_search_time')
    c['genres'] = pl.get('genres')

def handle(event, context):

    from pprint import pprint
    pprint(event)
    # Get single channel from query param "id"
    if event and "queryStringParameters" in event:
        if event["queryStringParameters"] and "id" in event["queryStringParameters"]:
            id = event['queryStringParameters']['id']
            c = mirrorfm_channels.query(
                KeyConditionExpression=Key('host').eq('yt') & Key('channel_id').eq(id))["Items"][0]
            pl = mirrorfm_yt_playlists.query(
                KeyConditionExpression=Key('yt_channel_id').eq(id) & Key('num').eq(1))["Items"][0]
            populate(c, pl)
            return {
                'statusCode': 200,
                'body': json.dumps(c),
                'headers': {
                    'Content-Type': 'application/json',
                    'Access-Control-Allow-Origin': '*'
                }
            }

    # Get all channels
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
    channels = mirrorfm_channels.query(
        KeyConditionExpression=Key('host').eq('yt'))["Items"]

    # channel doesn't have a name yet
    channels = [c for c in channels if 'channel_name' in c]

    for c in channels:
        if c['channel_id'] not in playlists_map:
            # channel doesn't have a playlist yet
            continue
        populate(c, playlists_map[c['channel_id']])

    arr["youtube"]["channels"] = channels
    return {
        'statusCode': 200,
        'body': json.dumps(arr),
        'headers': {
            'Content-Type': 'application/json',
            'Access-Control-Allow-Origin': '*'
        }
    }


if __name__ == "__main__":
    pprint(handle({}, {}))
