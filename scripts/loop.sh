#!/bin/sh
# Generic loop wrapper for long-running k3s workers.
# Runs the given command, sleeps, and repeats.
# Each iteration is a fresh process (clean connections, no state leaks).
# If the command fails, the loop continues after sleeping.

INTERVAL="${LOOP_INTERVAL:-60}"

while true; do
    "$@" || echo "[loop] Process exited with error, retrying in ${INTERVAL}s..."
    sleep "$INTERVAL"
done
