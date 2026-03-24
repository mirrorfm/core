#!/usr/bin/env python3
"""Unit tests for similarity.py — track matching logic."""

import sys
import os

sys.path.insert(0, os.path.dirname(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "trackfilter"))

from similarity import (
    TRACK_SIMILARITY_THRESHOLD,
    clean_artist,
    sanitize,
    artist_combinations,
    is_match,
)


def test_sanitize():
    # Basic lowercasing
    assert sanitize("Daft Punk - One More Time") == "daft punk one more time"

    # Strips excludes
    assert sanitize("Track - Original Mix") == "track"
    assert sanitize("Track - Extended Version") == "track"
    assert sanitize("Track - Radio Edit") == "track"
    assert sanitize("Track - Remastered") == "track"

    # Strips feat/ft
    assert sanitize("Track (feat. Someone)") == "track"
    assert sanitize("Track (ft. Someone)") == "track"

    # Normalizes & and "and"
    assert sanitize("Harley & Muscle") == "harley muscle"
    assert sanitize("Harley and Muscle") == "harley muscle"
    assert sanitize("Harley&Muscle") == "harley muscle"

    # Does NOT strip remix/edit credits (different creative works)
    assert "kaytranada" in sanitize("Track - Kaytranada Edit")
    assert "dub" in sanitize("Track - Dub Mix")

    print("  sanitize: PASS")


def test_clean_artist():
    assert clean_artist("Népal archived") == "Népal"
    assert clean_artist("Agent Orange aka Cari Lekebusch") == "Agent Orange"
    assert clean_artist("Daft Punk") == "Daft Punk"
    assert clean_artist("ARCHIVED") == "ARCHIVED"  # only strips " archived" suffix

    print("  clean_artist: PASS")


def test_artist_combinations():
    combos = artist_combinations(["Daft Punk"])
    assert "Daft Punk" in combos

    combos = artist_combinations(["Black Loops", "James Pepper"])
    assert "Black Loops" in combos
    assert "James Pepper" in combos
    assert "Black Loops James Pepper" in combos

    print("  artist_combinations: PASS")


def test_is_match_basic():
    # Exact match
    passes, score = is_match("Daft Punk", "One More Time", ["Daft Punk"], "One More Time")
    assert passes and score >= 0.99

    # Wrong match
    passes, score = is_match("Gucci Mane", "Party animal", ["Unnikrishnan"], "Innisai Paadivarum")
    assert not passes

    print("  is_match basic: PASS")


def test_is_match_excludes():
    passes, _ = is_match("Dawn Again", "Me 4 U", ["Dawn Again"], "Me 4 U - Original Mix")
    assert passes

    print("  is_match excludes: PASS")


def test_is_match_multi_artist_spotify():
    # YT has both artists concatenated, Spotify lists them separately
    passes, score = is_match(
        "Black Loops James Pepper", "Three Drops",
        ["Black Loops", "James Pepper"], "Three Drops",
    )
    assert passes and score >= 0.99

    # YT has one artist, Spotify has two
    passes, score = is_match(
        "Cosmonection", "Cocktail",
        ["Cosmonection", "Tour-Maubourg"], "Cocktail",
    )
    assert passes and score >= 0.99

    print("  is_match multi-artist Spotify: PASS")


def test_is_match_first_artist_fallback():
    # trackfilter splits "Harley & Muscle" → ["Harley", "Muscle"]
    # joined: "Harley Muscle", first: "Harley"
    passes, _ = is_match(
        "Harley Muscle", "With Me",
        ["Harley&Muscle"], "With Me",
        first_yt_artist="Harley",
    )
    assert passes

    print("  is_match first-artist fallback: PASS")


def test_is_match_and_normalization():
    passes, _ = is_match(
        "Yoha The Dragon Tribe", "25 Years",
        ["Yoha and the Dragon Tribe"], "25 Years Old",
    )
    assert passes

    print("  is_match 'and' normalization: PASS")


def test_is_match_creative_works_rejected():
    # Edit by another artist — should NOT pass
    passes, _ = is_match(
        "Aaliyah", "Rock The Boat (Kaytranada Edit)",
        ["Aaliyah"], "Rock The Boat",
    )
    assert not passes

    # Different track entirely
    passes, _ = is_match(
        "Childish Gambino", "3005",
        ["Childish Gambino"], "Redbone",
    )
    assert not passes

    print("  is_match creative works rejected: PASS")


def test_is_match_aka():
    passes, score = is_match(
        "Agent Orange", "Give A Little More Love",
        ["Agent Orange aka Cari Lekebusch"], "Give a Little More Love",
    )
    assert passes and score >= 0.99

    print("  is_match aka: PASS")


if __name__ == "__main__":
    print("Running similarity tests...")
    test_sanitize()
    test_clean_artist()
    test_artist_combinations()
    test_is_match_basic()
    test_is_match_excludes()
    test_is_match_multi_artist_spotify()
    test_is_match_first_artist_fallback()
    test_is_match_and_normalization()
    test_is_match_creative_works_rejected()
    test_is_match_aka()
    print("\nALL TESTS PASSED")
