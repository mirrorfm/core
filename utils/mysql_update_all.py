#!/usr/bin/python3.7

import boto3
import sys
import logging
from decimal import Decimal
import decimal
from datetime import datetime
import json
import os
import pymysql

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
channels = cursor.fetchall()

for c in channels:
    all = json.loads(c['thumbnails'])
    cursor.execute('UPDATE yt_channels SET thumbnail_high=%s, thumbnail_medium=%s, thumbnail_default=%s WHERE id=%s',
                   [all['high']['url'], all['medium']['url'], all['default']['url'], c['id']])
    conn.commit()