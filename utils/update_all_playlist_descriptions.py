#!/usr/bin/python3.7

"""
Set all playlist descriptions.

Example result:

0M7IiBVBquus5YkeV4xEyt YouTube channel with minimal dub, industrial techno, minimal techno. Add your own on www.mirror.fm #mirrorfm
7xbwKyY3vYNFGunL35KtYo YouTube channel with industrial techno. Add your own on www.mirror.fm #mirrorfm
6LFZ6taEfZ0i7VWnZl8u4Z YouTube channel. Add your own on www.mirror.fm #mirrorfm
"""

import boto3
from collections import OrderedDict
from operator import itemgetter
import spotipy

client = boto3.client("dynamodb", region_name='eu-west-1')
dynamodb = boto3.resource("dynamodb", region_name='eu-west-1')
mirrorfm_yt_playlists = dynamodb.Table('mirrorfm_yt_playlists')

sp = spotipy.Spotify(auth_manager=spotipy.SpotifyOAuth(scope='playlist-modify-public playlist-modify-private',
                                                       username="xlqeojt6n7on0j7coh9go8ifd"))

# Get all
playlists = mirrorfm_yt_playlists.scan()
print(len(playlists['Items']))

for p in playlists['Items']:
    # Get top 3 genres
    genres = p.get('genres')
    sorted_genres = OrderedDict(sorted(genres.items(), key=itemgetter(1), reverse=True))
    top3_genres = list(sorted_genres)[:3]

    # Description
    genres_str = ''
    if len(top3_genres) > 0:
        genres_str = ' with ' + ', '.join(top3_genres)
    desc = "YouTube channel" + genres_str + ". Add your own on www.mirror.fm #mirrorfm"

    # Update
    print(p.get('spotify_playlist'))
    sp.playlist_change_details(p.get('spotify_playlist'), description=desc)
