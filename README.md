# Mirror.FM

Sync YouTube music channels with Spotify playlists

--------

This repository contains the code for the main background processes.
If you are looking for the [website](https://mirror.fm) code, see https://github.com/mirrorfm/www
If you are looking to add your own channels, see https://github.com/mirrorfm/data

## Features

 - Automatically builds Spotify playlists from any YouTube music channel,
 - Constantly check for new YouTube uploads,
 - Find previously unreleased YouTube songs that were released on Spotify today.

## How it works

2 CRON jobs running on AWS Lambda:

 - λ0 [`from-github`](functions/from-github/)
 - λ1 [`from-youtube`](functions/from-youtube/)
 - λ2 [`to-spotify`](functions/to-spotify/)
 - λ3 [`to-www`](functions/to-www/)