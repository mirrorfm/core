#!/usr/bin/env python3
"""
k3s runner — replaces loop.sh for Python functions.

Calls handle() in a tight loop within a single process so connections
(MySQL, DynamoDB, Spotify) are reused across iterations.

Backs off only when needed:
  - No work found  → SHORT_IDLE  (5 s, configurable)
  - Rate / API err → exponential backoff up to MAX_BACKOFF
  - Other error    → MIN_BACKOFF then retry

handle() must return a dict with a "searched" key (>0 means work was done).
Lambda ignores the return value, so this is fully compatible.
"""

import importlib
import os
import sys
import time
import traceback

MIN_INTERVAL = int(os.getenv("MIN_INTERVAL", "1"))
SHORT_IDLE = int(os.getenv("SHORT_IDLE", "5"))
MIN_BACKOFF = int(os.getenv("MIN_BACKOFF", "5"))
MAX_BACKOFF = int(os.getenv("MAX_BACKOFF", "300"))

RATE_LIMIT_MARKERS = ["rate limit", "429", "retry after", "too many requests"]


def is_rate_limit(exc):
    msg = str(exc).lower()
    return any(m in msg for m in RATE_LIMIT_MARKERS)


def run():
    mod = importlib.import_module("main")
    handle = mod.handle

    backoff = 0

    while True:
        try:
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
