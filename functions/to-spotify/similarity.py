"""
Track similarity matching for music.

Pure music matching logic — no knowledge of Spotify API, DynamoDB, or any
data structures. Works only with artist names, track names, and strings.

Long-term target: move to trackfilter library.
"""

import re
from difflib import SequenceMatcher

TRACK_SIMILARITY_THRESHOLD = 0.8
TRACK_SIMILARITY_EXCLUDES = [
    "radio version",
    "original mix",
    "original version",
    "instrumental",
    "remix",
    "extended version",
    "extended mix",
    "radio edit",
    "remastered",
    "remaster",
    "short version",
    "shorter edit",
    "filtered version",
]


def similar(a, b):
    """SequenceMatcher ratio between two strings."""
    return SequenceMatcher(None, a, b).ratio()


def clean_artist(artist_name):
    """
    Clean an artist name for comparison.
    Strips 'archived' suffix and 'aka' aliases.
    """
    artist_name = re.sub(r'\s+archived$', '', artist_name, flags=re.IGNORECASE)
    artist_name = re.sub(r'\s+\b(aka|a\.k\.a\.?)\b.*$', '', artist_name, flags=re.IGNORECASE)
    return artist_name


def sanitize(track):
    """
    Clean a track string for similarity comparison.
    Applied identically to both sides of the comparison.
    """
    track = track.lower()
    # Remove generic suffixes that don't change the track identity
    for sub in TRACK_SIMILARITY_EXCLUDES:
        track = track.replace(sub, "")
    # Strip featured artist credits
    track = re.sub(r"\(?\b(feat\.?|ft\.?|featuring)\b[^)]*\)?", "", track)
    # Normalize artist connectors: &, "and" are interchangeable in music
    track = track.replace("&", " ")
    track = re.sub(r"\band\b", " ", track)
    # Keep alphanumeric + spaces, normalize whitespace
    track = "".join(ch for ch in track if ch.isalnum() or ch == " ")
    track = " ".join(track.split())
    return track


def artist_combinations(artist_names):
    """
    Build multiple artist name combinations for comparison.

    Given ["Black Loops", "James Pepper"], returns:
    {"Black Loops", "James Pepper", "Black Loops James Pepper"}

    Args:
        artist_names: list of artist name strings (already cleaned)

    Returns:
        set of name combinations
    """
    combos = set()
    for i in range(len(artist_names)):
        combos.add(artist_names[i])
        combos.add(" ".join(artist_names[:i + 1]))
    combos.add(" ".join(artist_names))
    return combos


def is_match(yt_artists, yt_track, sp_artists, sp_track, first_yt_artist=None):
    """
    Check if a YouTube track matches a candidate track.

    Args:
        yt_artists: joined artist string from YouTube (e.g. "Black Loops James Pepper")
        yt_track: track name from YouTube (e.g. "Three Drops")
        sp_artists: list of artist name strings from the candidate (e.g. ["Black Loops", "James Pepper"])
        sp_track: track name from the candidate (e.g. "Three Drops")
        first_yt_artist: first YouTube artist only, for multi-artist fallback (e.g. "Black Loops")

    Returns:
        (passes, score) — whether it passes the threshold, and the similarity score
    """
    # Clean candidate artists and build combinations
    sp_artists_clean = [clean_artist(a) for a in sp_artists]
    sp_combos = artist_combinations(sp_artists_clean)

    # Build all "Artist - Track" candidates
    candidates = [f"{combo} - {sp_track}" for combo in sp_combos]

    # Compare full YT string against all candidates
    yt_full = f"{yt_artists} - {yt_track}"
    s1 = sanitize(yt_full)
    best_score = max(similar(s1, sanitize(c)) for c in candidates)

    if best_score >= TRACK_SIMILARITY_THRESHOLD:
        return True, best_score

    # First-artist fallback for multi-artist YouTube titles
    if first_yt_artist and first_yt_artist != yt_artists:
        yt_first = f"{first_yt_artist} - {yt_track}"
        s1_first = sanitize(yt_first)
        score = max(similar(s1_first, sanitize(c)) for c in candidates)
        if score > best_score:
            best_score = score

    return best_score >= TRACK_SIMILARITY_THRESHOLD, best_score
