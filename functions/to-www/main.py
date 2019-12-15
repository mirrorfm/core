#!/usr/bin/python3.7

import boto3
from pprint import pprint
from boto3.dynamodb.conditions import Key
import json

client = boto3.client("dynamodb", region_name='eu-west-1')
dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
mirrorfm_channels = dynamodb.Table('mirrorfm_channels')


def get_table_count(table_name):
    return client.describe_table(TableName=table_name)["Table"]["ItemCount"]


def handle(event, context):
    arr = {
        "youtube": {
            "channels": [],
            "total_channels": get_table_count('mirrorfm_channels'),
            "total_tracks": get_table_count('mirrorfm_yt_tracks'),
            "broken_tracks": 0,
            "found_tracks": get_table_count('mirrorfm_yt_duplicates')
        }
    }
    all_channels = mirrorfm_channels.query(
        KeyConditionExpression=Key('host').eq('yt'))
    arr["youtube"]["channels"] = all_channels["Items"]
    return arr


if __name__ == "__main__":
    handle({}, {})