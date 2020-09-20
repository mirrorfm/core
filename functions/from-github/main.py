#!/usr/bin/python3.7

import os
import sys

# https://github.com/apex/apex/issues/639#issuecomment-455883587
file_path = os.path.dirname(__file__)
module_path = os.path.join(file_path, "env")
sys.path.append(module_path)

from pprint import pprint
import urllib.request
import boto3
import pymysql

AWS_ACCOUNT_ID = os.getenv('AWS_ACCOUNT_ID')
AWS_REGION = "eu-west-1"

sns = boto3.client('sns', region_name=AWS_REGION)
topic_arn = 'arn:aws:sns:' + AWS_REGION + ':' + AWS_ACCOUNT_ID + ':mirrorfm_incoming_youtube_channel'

dynamodb = boto3.resource("dynamodb", region_name=AWS_REGION)
mirrorfm_cursors = dynamodb.Table('mirrorfm_cursors')


name = os.getenv('DB_USERNAME')
password = os.getenv('DB_PASSWORD')
db_name = os.getenv('DB_NAME')
host = os.getenv('DB_HOST')


try:
    conn = pymysql.connect(host,
                           user=name,
                           passwd=password,
                           db=db_name,
                           connect_timeout=5,
                           cursorclass=pymysql.cursors.DictCursor)
except pymysql.MySQLError as e:
    print("ERROR: Unexpected error: Could not connect to MySQL instance.")
    sys.exit()


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
            'name': 'from_github_last_successful_channel'
        },
        AttributesToGet=[
            'value'
        ]
    )

    if 'Item' in last_successful_entry and last_successful_entry['Item'] != {}:
        current = int(last_successful_entry['Item']['value']) + 1
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
            cur = conn.cursor()
            cur.execute('insert into yt_channels (channel_id) values(%s)', [channel_id])
            conn.commit()
            print('Added', channel_name)
            response = sns.publish(
                TopicArn=topic_arn,
                Message=channel_id,
            )
            print('SNS', response)
        except pymysql.IntegrityError:
            print('Duplicate', channel_name, '(nothing to do)')

        current += 1

    mirrorfm_cursors.put_item(
        Item={
            'name': 'from_github_last_successful_channel',
            'value': current - 1
        }
    )


if __name__ == "__main__":
    handle({'repository': {'full_name': 'mirrorfm/data'}, 'head_commit': {'modified': ["youtube-channels.csv"]}}, {})