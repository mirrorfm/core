# Mirror.FM

Sync Discogs labels or YouTube music channels with Spotify playlists.

--------

This repository contains the background worker functions.

 - Website: https://github.com/mirrorfm/www
 - Submit channels/labels: https://github.com/mirrorfm/data

## Features

 - Automatically builds Spotify playlists from any YouTube music channel or Discogs label
 - Constantly checks for new uploads/releases
 - Finds previously unreleased songs that were released on Spotify today
 - Provides backups for deleted, hacked or terminated YouTube channels (archived playlists)

## Functions

| Function | Runtime | Description |
|---|---|---|
| [`from-github`](functions/from-github/) | Go | Webhook handler for GitHub data submissions |
| [`from-youtube`](functions/from-youtube/) | Python | Fetches YouTube channel data |
| [`from-discogs`](functions/from-discogs/) | Go | Fetches Discogs label data |
| [`to-spotify`](functions/to-spotify/) | Python | Searches and adds tracks to Spotify |
| [`to-www`](functions/to-www/) | Go | REST API serving the frontend |
| [`manage-playlists`](functions/manage-playlists/) | Go | Sorts, archives, and repairs playlists |

## Deployment

- **k3s** (primary): `from-youtube`, `from-discogs`, `to-spotify`, `manage-playlists` — deployed via [deploy-k3s.yml](.github/workflows/deploy-k3s.yml)
- **AWS Lambda** (cloud-only): `to-www`, `from-github` — deployed via [deploy-lambda.yml](.github/workflows/deploy-lambda.yml)
- **AWS Lambda** (fallback): k3s functions are also deployed to Lambda as fallback, managed by [homeplane](https://github.com/mirrorfm/homeplane)
- **Infrastructure**: Terraform in [`terraform/`](terraform/) — deployed via [terraform.yml](.github/workflows/terraform.yml)
