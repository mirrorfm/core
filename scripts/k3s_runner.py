#!/usr/bin/env python3
"""
k3s runner — replaces loop.sh for Python functions.

Calls handle() in a tight loop within a single process so connections
(MySQL, DynamoDB, Spotify) are reused across iterations.

Priority: SQS messages first (instant processing of new submissions),
then cursor-based polling as fallback.

Backs off only when needed:
  - No work found  → SHORT_IDLE  (5 s, configurable)
  - Rate / API err → exponential backoff up to MAX_BACKOFF
  - Other error    → MIN_BACKOFF then retry

handle() must return a dict with a "searched" key (>0 means work was done).
Lambda ignores the return value, so this is fully compatible.
"""

import importlib
import json
import os
import sys
import time
import traceback

import boto3

MIN_INTERVAL = int(os.getenv("MIN_INTERVAL", "1"))
SHORT_IDLE = int(os.getenv("SHORT_IDLE", "5"))
MIN_BACKOFF = int(os.getenv("MIN_BACKOFF", "5"))
MAX_BACKOFF = int(os.getenv("MAX_BACKOFF", "300"))
SQS_QUEUE_URL = os.getenv("SQS_QUEUE_URL", "")
SQS_POLL_WAIT = int(os.getenv("SQS_POLL_WAIT", "5"))

RATE_LIMIT_MARKERS = ["rate limit", "429", "retry after", "too many requests"]

sqs = boto3.client("sqs", region_name="eu-west-1") if SQS_QUEUE_URL else None


def is_rate_limit(exc):
    msg = str(exc).lower()
    return any(m in msg for m in RATE_LIMIT_MARKERS)


def poll_sqs():
    """Try to receive one SQS message. Returns (event_dict, receipt_handle) or (None, None)."""
    if not sqs or not SQS_QUEUE_URL:
        return None, None

    resp = sqs.receive_message(
        QueueUrl=SQS_QUEUE_URL,
        MaxNumberOfMessages=1,
        WaitTimeSeconds=SQS_POLL_WAIT,
    )
    messages = resp.get("Messages", [])
    if not messages:
        return None, None

    msg = messages[0]
    receipt = msg["ReceiptHandle"]
    body = json.loads(msg["Body"])

    # SNS-wrapped message (from SNS → SQS subscription)
    if "Type" in body and body["Type"] == "Notification":
        entity_id = body["Message"]
        event = {"Records": [{"Sns": {"Message": entity_id}}]}
        print(f"[sqs] Received SNS event: {entity_id}")
        return event, receipt

    # Direct SQS message (from from-youtube/from-discogs → to-spotify)
    if "host" in body and "entity_id" in body:
        event = {"sqs_entity": body}
        print(f"[sqs] Received entity event: {body}")
        return event, receipt

    print(f"[sqs] Unknown message format, skipping: {body}")
    return None, receipt


def run():
    mod = importlib.import_module("main")
    handle = mod.handle

    backoff = 0

    while True:
        try:
            # Priority: check SQS for event-driven work
            event, receipt = poll_sqs()
            if event:
                result = handle(event, {})
                if receipt:
                    sqs.delete_message(QueueUrl=SQS_QUEUE_URL, ReceiptHandle=receipt)
                backoff = 0
                time.sleep(MIN_INTERVAL)
                continue

            # Fallback: cursor-based polling
            result = handle({}, {})

            searched = 0
            if isinstance(result, dict):
                searched = result.get("searched", 0)

            if searched > 0:
                backoff = 0
                time.sleep(MIN_INTERVAL)
            else:
                # No work — idle briefly before checking next entity
                time.sleep(SHORT_IDLE)

        except Exception as e:
            if is_rate_limit(e):
                backoff = min(max(backoff * 2, MIN_BACKOFF), MAX_BACKOFF)
                print(f"[runner] Rate limited, backing off {backoff}s: {e}")
            else:
                backoff = MIN_BACKOFF
                print(f"[runner] Error, retrying in {backoff}s:")
                traceback.print_exc()

            time.sleep(backoff)


if __name__ == "__main__":
    run()
