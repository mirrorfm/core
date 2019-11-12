### Purpose

Fetches YouTube channels and their tracks

### Triggers

 - Cloudwatch Event (CRON)
 - New item added to "Channels" DynamoDB table

### Run locally

    virtualenv ./.venv
    source ./.venv/bin/activate
    pip3 install -r requirements.txt
    YT_DEVELOPER_KEY=<YT_DEVELOPER_KEY> python3 main.py
    deactivate

### Build and deploy

    apex build from-youtube >/dev/null && apex deploy from-youtube --region eu-west-1 -ldebug --env-file ./functions/from-youtube/env.json

### AWS prerequesites

- DynamoDB table:
    - CursorsTable
        - name: `mirrorfm_cursors`
        - partition key: `name` (string)
    - ChannelsTable
        - name: `mirrorfm_channels`
        - partition key: `host` (string) (i.e. yt)
        - sort_key: `channel_id` (string)
    - TracksTable
        - name: `mirrorfm_yt_tracks`
        - partition key: `yt_channel_id` (string)
        - sort_key: `yt_track_composite` (string) (`{yt_published_at}-{yt_track_id}`)
- IAM role:
    - name: `apex_lambda_function`
    - permissions: IAM, DynamoDB, Lambda