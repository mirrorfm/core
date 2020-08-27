#!/usr/bin/python3.7

import os
import sys
from pprint import pprint
import urllib.request
import boto3
from botocore.exceptions import ClientError

dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
mirrorfm_channels = dynamodb.Table('mirrorfm_channels')
mirrorfm_cursors = dynamodb.Table('mirrorfm_cursors')


def handle(event, context):
    repo = event['repository']['full_name']
    file = event['head_commit']['modified'][0]

    if file != "youtube-channels.csv":
        return

    url = '/'.join(['https://raw.githubusercontent.com', repo, 'master', file])
    print(url)

    lines = urllib.request.urlopen(url).readlines()

    last_successful_entry = mirrorfm_cursors.get_item(
        Key={
            'name': 'last_successful_entry'
        },
        AttributesToGet=[
            'value'
        ]
    )

    if 'Item' in last_successful_entry and last_successful_entry['Item'] != {}:
        current = int(last_successful_entry['Item']['value'])
    else:
        # no cursor, query first
        current = 1  # file has headers

    total = len(lines) - 1

    while current <= total:
        print(current, "/", total)

        current_line = lines[current]
        line = str(current_line, 'utf-8').split(',')
        channel_id = line[0]
        channel_name = line[1]

        if not channel_id or channel_id == "":
            print("line", current, "is empty")
            break

        try:
            mirrorfm_channels.put_item(
                Item={
                    'host': 'yt',
                    'channel_id': channel_id
                },
                ConditionExpression='attribute_not_exists(yt) and attribute_not_exists(channel_id)'
            )
            print('Added', channel_name)
        except ClientError:
            print('Duplicate', channel_name, '(nothing to do)')

        current += 1

    mirrorfm_cursors.put_item(
        Item={
            'name': 'last_successful_entry',
            'value': current - 1
        }
    )


if __name__ == "__main__":
    handle({'repository': {'full_name': 'mirrorfm/data'}, 'head_commit': {'modified': ["youtube-channels.csv"]}}, {})