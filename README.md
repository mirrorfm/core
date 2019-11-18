# Mirror.FM

Sync YouTube music channels with Spotify playlists

## Features

 - Automatically builds Spotify playlists from any YouTube music channel,
 - Constantly check for new YouTube uploads,
 - Find previously unreleased YouTube songs that were released on Spotify today.

## How it works

2 CRON jobs running on AWS Lambda:

 - λ0 [`from-github`](functions/from-github/)
 - λ1 [`from-youtube`](functions/from-youtube/)
 - λ2 [`to-spotify`](functions/to-spotify/)