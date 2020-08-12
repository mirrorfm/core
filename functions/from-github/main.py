#!/usr/bin/python3.7

import os
import sys
from pprint import pprint
import urllib.request
import boto3
import botocore
from botocore.exceptions import ClientError

dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
mirrorfm_channels = dynamodb.Table('mirrorfm_channels')


def handle(event, context):
    repo = event['repository']['full_name']
    file = event['head_commit']['modified'][0]

    URL = '/'.join(['https://raw.githubusercontent.com', repo, 'master', file])
    print(URL)

    lines = urllib.request.urlopen(URL).readlines()
    last = lines[len(lines) - 1]

    channel_id = str(last, 'utf-8').split(',')[0]
    print(channel_id)

    if file == "youtube-channels.csv":
        try:
            mirrorfm_channels.put_item(
                Item={
                    'host': 'yt',
                    'channel_id': channel_id
                },
                ConditionExpression='attribute_not_exists(yt) and attribute_not_exists(channel_id)'
            )
        except ClientError:
            print('Duplicate', channel_id, '(nothing to do)')


if __name__ == "__main__":
    handle({'repository': {'full_name': 'mirrorfm/data'}, 'head_commit': {'modified': ["youtube-channels.csv"]}}, {})