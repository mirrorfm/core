# Mirror.FM

Sync discogs labels or music youtube channels with Spotify playlists

--------

This repository contains the code for the main background processes.

 - If you are looking for the [website](https://mirror.fm) code, see https://github.com/mirrorfm/www
 - If you are looking to add your own channels, see https://github.com/mirrorfm/data

## Features

 - Automatically builds Spotify playlists from any youtube music channel/discogs label,
 - Constantly check for new uploads/releases,
 - Find previously unreleased songs that were released on Spotify today,
 - Provide backups for deleted, hacked or terminated YouTube channels.

## Lambdas

 - [`from-github`](functions/from-github/)
 - [`from-discogs`](functions/from-discogs/)
 - [`from-youtube`](functions/from-youtube/)
 - [`to-spotify`](functions/to-spotify/)
 - [`to-www`](functions/to-www/)
