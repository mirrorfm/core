#!/usr/bin/python3.7

import boto3
from pprint import pprint
from boto3.dynamodb.conditions import Key
import json
from pprint import pprint

client = boto3.client("dynamodb", region_name='eu-west-1')
dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
mirrorfm_channels = dynamodb.Table('mirrorfm_channels')
mirrorfm_events = dynamodb.Table('mirrorfm_events')

MAX = 20

def handle(event, context):
    arr = {}
    channels = mirrorfm_channels.scan()
    channels_map = {c['channel_id']: c for c in channels['Items']}
    events = mirrorfm_events.query(
        KeyConditionExpression=Key('host').eq('yt'),
        ScanIndexForward=False,
        Limit=MAX
    )["Items"]
    for e in events:
        c = channels_map[e['channel_id']]
        e['channel_name'] = c.get('channel_name')
    arr['events'] = events
    arr['total'] = MAX
    return arr


if __name__ == "__main__":
    pprint(handle({}, {}))
