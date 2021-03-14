### Purpose

- Indefinitely search for stored songs on Spotify
- Create Spotify playlists for channels and add songs to them

### Triggers

 - Cloudwatch Event (CRON)
 - Batches of items added to "Tracks" DynamoDB table

### Generate a Spotify token

Before running locally or deploying this, use `token_gen.py` to generate and store a Spotify token into DynamoDB.

### Run locally

    virtualenv ./.venv
    source ./.venv/bin/activate
    pip3 install -r requirements.txt
    python token_gen.py
    SPOTIPY_CLIENT_ID=<id> SPOTIPY_CLIENT_SECRET=<secret> SPOTIPY_USER=<user> python3 main.py
    deactivate

### Build and deploy

Fill in `./functions/to-spotify/env.json` with `SPOTIPY_CLIENT_ID` and `SPOTIPY_CLIENT_SECRET`, and then:

    apex build to-spotify >/dev/null && apex deploy to-spotify --region eu-west-1 -ldebug --env-file ./functions/to-spotify/env.json

### AWS prerequisites

 - All from λ1