### Purpose

Fetches YouTube channels and their tracks

### Triggers

 - Cloudwatch Event (CRON)
 - New item added to "Channels" DynamoDB table

### Commands

    make setup
    make from-youtube
    make deploy-to-youtube

### AWS prerequesites

- DynamoDB table:
    - CursorsTable
        - name: `mirrorfm_cursors`
        - partition key: `name` (string)
    - TracksTable
        - name: `mirrorfm_yt_tracks`
        - partition key: `yt_channel_id` (string)
        - sort_key: `yt_track_composite` (string) (`{yt_published_at}-{yt_track_id}`)
- IAM role:
    - name: `apex_lambda_function`
    - permissions: IAM, DynamoDB, Lambda