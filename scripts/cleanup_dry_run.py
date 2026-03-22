#!/usr/bin/env python3
"""
Dry-run cleanup script for mirror.fm Spotify playlists.

Scans YouTube tracks matched BEFORE the 0.8 similarity threshold was added
(2023-12-16), re-evaluates each match, and reports what actions would be taken.

Usage:
    pip install boto3 spotipy
    pip install -e /path/to/trackfilter
    python cleanup_dry_run.py [--channels N] [--channel-id ID ...]
"""

import argparse
import json
import os
import re
import sys
from collections import defaultdict
from datetime import datetime
from difflib import SequenceMatcher

import boto3
from boto3.dynamodb.conditions import Key, Attr

# Add trackfilter to path
TRACKFILTER_PATH = os.path.join(os.path.dirname(__file__), "..", "..", "trackfilter")
sys.path.insert(0, TRACKFILTER_PATH)
from trackfilter.cli import split_artist_track

# --- Constants (mirrored from to-spotify/main.py) ---

TRACK_SIMILARITY_THRESHOLD = 0.8
TRACK_SIMILARITY_EXCLUDES = [
    "radio version",
    "original mix",
    "original version",
    "club mix",
    "instrumental",
    "remix",
]

# Date when the similarity check was added (commit 6a192e0)
SIMILARITY_FEATURE_DATE = "2023-12-16T23:05:38+00:00"


# --- Similarity logic (same as to-spotify/main.py) ---

def similar(a, b):
    return SequenceMatcher(None, a, b).ratio()


def sanitize(track):
    track = "".join(ch for ch in track if ch.isalnum()).lower()
    for sub in TRACK_SIMILARITY_EXCLUDES:
        track = track.replace(sub, "")
    return track


def reparse_youtube_title(yt_track_name):
    """Re-run trackfilter on the original YouTube title to get artist + track."""
    result = split_artist_track(yt_track_name)
    if result and len(result) > 1:
        artists, track = result
        artist = artists[0] if isinstance(artists, list) else artists
        return artist.strip(), track.strip()
    return None, None


def evaluate_match(yt_title, spotify_track_info):
    """
    Re-evaluate a previously matched track using the 0.8 similarity threshold.

    Returns dict with evaluation details.
    """
    # What trackfilter parses from the YouTube title
    parsed_artist, parsed_track = reparse_youtube_title(yt_title)

    if parsed_artist and parsed_track:
        track_name = f"{parsed_artist} - {parsed_track}"
    else:
        # No separator found — use raw title (same as to-spotify fallback)
        track_name = yt_title

    # What's in Spotify
    sp_artist = spotify_track_info["artists"][0]["name"]
    sp_track = spotify_track_info["name"]
    found_track = f"{sp_artist} - {sp_track}"

    score = similar(sanitize(track_name), sanitize(found_track))

    return {
        "yt_title_original": yt_title,
        "parsed_query": track_name,
        "spotify_match": found_track,
        "spotify_uri": spotify_track_info.get("uri", spotify_track_info.get("external_urls", {}).get("spotify", "?")),
        "similarity": round(score, 4),
        "passes_threshold": score >= TRACK_SIMILARITY_THRESHOLD,
    }


# --- DynamoDB scanning ---

def get_channels_with_old_matches(dynamodb, limit=5, specific_ids=None):
    """
    Find YouTube channels that have tracks matched before the similarity feature.
    Returns channel IDs.
    """
    table = dynamodb.Table("mirrorfm_yt_tracks")

    if specific_ids:
        return specific_ids

    # Scan for distinct channel IDs with old matches.
    # We look for tracks that have spotify_uri but spotify_found_time < cutoff date
    # OR spotify_found_time doesn't exist (very old matches had no timestamp).
    channel_ids = set()
    scan_kwargs = {
        "FilterExpression": (
            Attr("spotify_uri").exists()
            & (
                Attr("spotify_found_time").not_exists()
                | Attr("spotify_found_time").lt(SIMILARITY_FEATURE_DATE)
            )
        ),
        "ProjectionExpression": "yt_channel_id",
    }

    while len(channel_ids) < limit:
        resp = table.scan(**scan_kwargs)
        for item in resp["Items"]:
            channel_ids.add(item["yt_channel_id"])
            if len(channel_ids) >= limit:
                break
        if "LastEvaluatedKey" not in resp:
            break
        scan_kwargs["ExclusiveStartKey"] = resp["LastEvaluatedKey"]

    return list(channel_ids)


def get_old_matched_tracks(dynamodb, channel_id):
    """
    Get all tracks for a channel that were matched before the similarity feature.
    """
    table = dynamodb.Table("mirrorfm_yt_tracks")

    tracks = []
    query_kwargs = {
        "KeyConditionExpression": Key("yt_channel_id").eq(channel_id),
        "FilterExpression": (
            Attr("spotify_uri").exists()
            & (
                Attr("spotify_found_time").not_exists()
                | Attr("spotify_found_time").lt(SIMILARITY_FEATURE_DATE)
            )
        ),
    }

    while True:
        resp = table.query(**query_kwargs)
        tracks.extend(resp["Items"])
        if "LastEvaluatedKey" not in resp:
            break
        query_kwargs["ExclusiveStartKey"] = resp["LastEvaluatedKey"]

    return tracks


def get_channel_name(channel_id):
    """Try to get channel name from MySQL. Falls back to channel_id."""
    # We don't want to depend on MySQL for the dry-run, so just return the ID.
    # The script user can cross-reference if needed.
    return channel_id


# --- Reporting ---

def print_report(results):
    """Print a summary report of the dry-run evaluation."""
    total = 0
    would_remove = 0
    would_keep = 0
    by_channel = defaultdict(lambda: {"remove": [], "keep": [], "total": 0})

    for channel_id, evaluations in results.items():
        for ev in evaluations:
            total += 1
            by_channel[channel_id]["total"] += 1
            if ev["passes_threshold"]:
                would_keep += 1
                by_channel[channel_id]["keep"].append(ev)
            else:
                would_remove += 1
                by_channel[channel_id]["remove"].append(ev)

    print("\n" + "=" * 80)
    print("DRY-RUN CLEANUP REPORT")
    print("=" * 80)
    print(f"\nTotal pre-0.8 tracks evaluated: {total}")
    print(f"  Would KEEP (>= {TRACK_SIMILARITY_THRESHOLD}):   {would_keep}")
    print(f"  Would REMOVE (< {TRACK_SIMILARITY_THRESHOLD}):  {would_remove}")
    if total > 0:
        print(f"  Removal rate: {would_remove / total * 100:.1f}%")

    for channel_id, data in by_channel.items():
        print(f"\n{'─' * 80}")
        print(f"Channel: {channel_id}")
        print(f"  Total pre-0.8 tracks: {data['total']}")
        print(f"  Would keep: {len(data['keep'])}  |  Would remove: {len(data['remove'])}")

        if data["remove"]:
            print(f"\n  WOULD REMOVE ({len(data['remove'])} tracks):")
            for ev in sorted(data["remove"], key=lambda e: e["similarity"]):
                print(f"    similarity={ev['similarity']:.2f}")
                print(f"      YT title:  {ev['yt_title_original']}")
                print(f"      Parsed as: {ev['parsed_query']}")
                print(f"      Matched:   {ev['spotify_match']}")
                print(f"      URI:       {ev['spotify_uri']}")
                print()

        if data["keep"]:
            # Show a few borderline keeps (lowest similarity among keeps)
            borderline = sorted(data["keep"], key=lambda e: e["similarity"])[:3]
            print(f"\n  BORDERLINE KEEPS (lowest similarity among kept):")
            for ev in borderline:
                print(f"    similarity={ev['similarity']:.2f}")
                print(f"      YT title:  {ev['yt_title_original']}")
                print(f"      Parsed as: {ev['parsed_query']}")
                print(f"      Matched:   {ev['spotify_match']}")
                print()

    # JSON output for further analysis
    print(f"\n{'=' * 80}")
    print("JSON OUTPUT (for further processing)")
    print("=" * 80)
    removal_list = []
    for channel_id, evaluations in results.items():
        for ev in evaluations:
            if not ev["passes_threshold"]:
                removal_list.append({
                    "channel_id": channel_id,
                    "spotify_uri": ev["spotify_uri"],
                    "similarity": ev["similarity"],
                    "yt_title": ev["yt_title_original"],
                    "spotify_match": ev["spotify_match"],
                })
    print(json.dumps(removal_list, indent=2))


def main():
    parser = argparse.ArgumentParser(
        description="Dry-run cleanup: re-evaluate old Spotify matches using 0.8 similarity threshold"
    )
    parser.add_argument(
        "--channels", type=int, default=5,
        help="Number of channels to evaluate (default: 5)"
    )
    parser.add_argument(
        "--channel-id", nargs="*",
        help="Specific channel IDs to evaluate (overrides --channels)"
    )
    args = parser.parse_args()

    dynamodb = boto3.resource("dynamodb", region_name="eu-west-1")

    print("Finding channels with pre-0.8 matched tracks...")
    channel_ids = get_channels_with_old_matches(
        dynamodb,
        limit=args.channels,
        specific_ids=args.channel_id,
    )
    print(f"Found {len(channel_ids)} channels: {channel_ids}")

    results = {}
    for channel_id in channel_ids:
        print(f"\nQuerying tracks for channel {channel_id}...")
        tracks = get_old_matched_tracks(dynamodb, channel_id)
        print(f"  Found {len(tracks)} pre-0.8 matched tracks")

        evaluations = []
        for track in tracks:
            yt_title = track.get("yt_track_name", "")
            spotify_info = track.get("spotify_track_info")

            if not yt_title or not spotify_info:
                # Can't evaluate without both pieces of data
                continue

            # spotify_track_info is stored as a DynamoDB map, should have artists and name
            if "artists" not in spotify_info or "name" not in spotify_info:
                continue

            ev = evaluate_match(yt_title, spotify_info)
            ev["yt_track_composite"] = track.get("yt_track_composite", "?")
            ev["spotify_playlist"] = track.get("spotify_playlist", "?")
            ev["spotify_found_time"] = track.get("spotify_found_time", "N/A")
            evaluations.append(ev)

        results[channel_id] = evaluations

    print_report(results)


if __name__ == "__main__":
    main()
