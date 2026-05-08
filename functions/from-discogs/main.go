package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/cenkalti/backoff/v4"
	_ "github.com/go-sql-driver/mysql"
	"github.com/irlndts/go-discogs"
	"github.com/pkg/errors"
)

type App struct {
	Discogs             discogs.Discogs
	DynamoDB            *dynamodb.DynamoDB
	DynamoDBTracksTable string
	DynamoDBCursorTable string
	SQLDriver           *sql.DB
	Backoff             backoff.BackOff
}

type LocalLabel struct {
	ID                  int
	LabelID             int
	HighestReleaseID    int `json:"highest_dg_release"`
	LabelReleases       int `json:"label_releases"`
	LabelTracks         int `json:"label_tracks"`
	MasterReleasesCache map[int]discogs.Release
	LastPage            int          `json:"last_page"`
	DidInit             sql.NullBool `json:"did_init"`
	MaxPages            int
}

func getApp() (App, error) {
	// MySQL
	dbHost := os.Getenv("DB_HOST")
	dbUser := os.Getenv("DB_USERNAME")
	dbPass := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	sqlDriver, err := sql.Open("mysql", dbUser+":"+dbPass+"@tcp("+dbHost+")/"+dbName+"?parseTime=true")
	if err != nil {
		return App{}, errors.Wrap(err, "failed to set up DB client")
	}

	// DynamoDB
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))
	dynamoClient := dynamodb.New(sess, &aws.Config{
		Region: aws.String("eu-west-1"),
	})

	// Discogs
	discogsToken := os.Getenv("DG_TOKEN")
	discogsClient, err := discogs.New(&discogs.Options{
		UserAgent: "Mirror.FM",
		Token:     discogsToken,
	})
	if err != nil {
		return App{}, errors.Wrap(err, "failed to set up discogs client")
	}

	return App{
		Discogs:             discogsClient,
		DynamoDB:            dynamoClient,
		DynamoDBTracksTable: "mirrorfm_dg_tracks",
		DynamoDBCursorTable: "mirrorfm_cursors",
		SQLDriver:           sqlDriver,
		Backoff:             backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 100),
	}, nil
}

func Handler(ctx context.Context, evt events.SNSEvent) error {
	app, err := getApp()
	if err != nil {
		return errors.Wrap(err, "failed to start up app")
	}

	var rowId int
	labelId, rowId, err := app.findNextLabelToProcess(evt)
	if err != nil {
		return errors.Wrap(err, "failed to find next label to process")
	}
	log.Printf("Processing labelId=%d rowId=%d", labelId, rowId)

	label, err := app.GetLabel(labelId)
	if errors.Is(err, ErrNotFound) {
		// Label was deleted/gone in Discogs. Skip it: advance the cursor so the
		// loop can move on to the next row instead of retrying this dead row
		// every 60s forever (which is what used to happen — see git history).
		log.Printf("Skipping unavailable label labelId=%d rowId=%d (404/410)", labelId, rowId)
		if rowId > 0 {
			return app.SaveCursor("from_discogs_last_successful_label", rowId)
		}
		return nil
	}
	if err != nil {
		return errors.Wrapf(err, "failed to retrieve label labelId=%d rowId=%d", labelId, rowId)
	}

	err = app.UpdateLabelWithThumbnail(label)
	if err != nil {
		return errors.Wrap(err, "failed to update label with thumbnail")
	}

	localLabel, err := app.GetLabelInfo(label.ID)
	if err != nil {
		return errors.Wrap(err, "failed to get label info")
	}

	for {
		releases, err := app.GetLabelReleases(localLabel.LastPage, labelId)
		if errors.Is(err, ErrNotFound) {
			// Label became unavailable mid-iteration (rare). Advance cursor and
			// move on; partial data we already wrote stays.
			log.Printf("Label became unavailable mid-pagination labelId=%d page=%d, skipping", labelId, localLabel.LastPage)
			break
		}
		if err != nil {
			return errors.Wrapf(err, "failed to get label releases labelId=%d page=%d", labelId, localLabel.LastPage)
		}

		localLabel.MaxPages = releases.Pagination.Pages

		log.Printf("Page %d/%d\n", localLabel.LastPage, localLabel.MaxPages)

		uniqueMasterReleases, skipped, err := app.populateUniqueMasterReleases(releases, localLabel)
		if err != nil {
			return errors.Wrap(err, "failed to populate unique master releases")
		}

		log.Printf("Skipped %d, kept: %+v\n", skipped, uniqueMasterReleases)

		err = app.persistReleasesTracks(localLabel, uniqueMasterReleases)
		if err != nil {
			return errors.Wrap(err, "failed to persist releases tracks")
		}

		localLabel.LastPage += 1
		isLastPage := localLabel.LastPage > localLabel.MaxPages

		// Save stats and cursors after each page, so lambda timeouts are no problem!
		err = app.UpdateLabelWithStats(localLabel, isLastPage)
		if err != nil {
			return err
		}

		if isLastPage {
			break
		}
	}

	// Notify to-spotify once after all pages are done
	if sqsURL := os.Getenv("SQS_TO_SPOTIFY_URL"); sqsURL != "" {
		sqsClient := sqs.New(session.Must(session.NewSession(&aws.Config{Region: aws.String("eu-west-1")})))
		body, _ := json.Marshal(map[string]interface{}{"host": "dg", "entity_id": labelId})
		sqsClient.SendMessage(&sqs.SendMessageInput{
			QueueUrl:    &sqsURL,
			MessageBody: aws.String(string(body)),
		})
		fmt.Println("Notified to-spotify via SQS")
	}

	if rowId > 0 {
		return app.SaveCursor("from_discogs_last_successful_label", rowId)
	}

	return nil
}

func (client *App) findNextLabelToProcess(evt events.SNSEvent) (int, int, error) {
	if len(evt.Records) > 0 {
		labelIdStr := evt.Records[0].SNS.Message

		res, err := strconv.Atoi(labelIdStr)
		if err != nil {
			return 0, 0, errors.Wrap(err, "failed to convert label LabelID to int")
		}

		return res, 0, nil
	}

	cursor, err := client.GetCursor("from_discogs_last_successful_label")
	if err != nil {
		return 0, 0, errors.Wrap(err, "failed to retrieve cursor")
	}
	fmt.Printf("last was %d\n", cursor)

	label, err := client.GetNextLabel(cursor)
	if err != nil {
		return 0, 0, err
	}

	return label.LabelID, label.ID, nil
}

func (client *App) populateUniqueMasterReleases(releases *discogs.LabelReleases, localLabel LocalLabel) ([]int, int, error) {
	var uniqueMasterReleases []int
	var skipped int

	for _, labelRelease := range releases.Releases {
		id := labelRelease.ID
		if isReleaseAlreadyStored(id, localLabel) {
			skipped += 1
			continue
		}
		localLabel.HighestReleaseID = id

		release, err := client.GetRelease(id)
		if errors.Is(err, ErrNotFound) {
			log.Printf("Skipping unavailable release id=%d (404/410)", id)
			continue
		}
		if err != nil {
			log.Printf("Skipping release %d due to error: %v", id, err)
			continue
		}

		var masterID int
		if release.MasterID == 0 {
			masterID = id
		} else {
			masterID = release.MasterID
		}

		if _, ok := localLabel.MasterReleasesCache[masterID]; !ok {
			uniqueMasterReleases = append(uniqueMasterReleases, masterID)
			localLabel.MasterReleasesCache[masterID] = *release
			log.Printf("%d => %d\n", id, masterID)
		}
	}
	return uniqueMasterReleases, skipped, nil
}

func (client *App) persistReleasesTracks(localLabel LocalLabel, uniqueMasterReleases []int) error {
	for _, masterReleaseId := range uniqueMasterReleases {
		if alreadyStored, err := client.isMasterReleaseAlreadyStored(localLabel.LabelID, masterReleaseId); err != nil {
			return err
		} else if alreadyStored {
			fmt.Println("Already stored")
			continue
		}

		fmt.Printf("tracks in %d %d\n", masterReleaseId, len(localLabel.MasterReleasesCache[masterReleaseId].Tracklist))
		err := client.AddTracks(localLabel.MasterReleasesCache[masterReleaseId], masterReleaseId, localLabel.LabelID)
		if err != nil {
			return err
		}

		localLabel.LabelReleases += 1
		localLabel.LabelTracks += len(localLabel.MasterReleasesCache[masterReleaseId].Tracklist)
	}

	return nil
}

func isReleaseAlreadyStored(releaseId int, localLabel LocalLabel) bool {
	// Here we assume that old releases that were recently added to a label
	// will have a new/high release LabelID.
	return localLabel.DidInit.Bool && releaseId <= localLabel.HighestReleaseID
}

// runK3sLoop is the long-running entrypoint for the k3s deployment.
//
// Mirrors scripts/k3s_runner.py (used by from-youtube/to-spotify): poll the
// SQS queue first so newly-submitted labels are picked up within seconds of
// the from-github webhook. If the queue is empty, fall back to the cursor-
// based sweep (CRON-equivalent — walks dg_labels by id) so we still catch up
// on anything missed during downtime.
//
// Without this loop the pod was running scripts/loop.sh, which is cursor-
// only — meaning new SNS notifications sat in the SQS queue forever (k3s
// doesn't read them, Lambda failover is disabled), and new submissions
// only got processed when the cursor walked to them, which could take days.
func runK3sLoop(ctx context.Context) {
	minInterval := getEnvIntOr("MIN_INTERVAL", 1)
	shortIdle := getEnvIntOr("SHORT_IDLE", 5)
	queueURL := os.Getenv("SQS_QUEUE_URL")
	pollWait := getEnvIntOr("SQS_POLL_WAIT", 5)

	var sqsClient *sqs.SQS
	if queueURL != "" {
		sess := session.Must(session.NewSession(&aws.Config{Region: aws.String("eu-west-1")}))
		sqsClient = sqs.New(sess)
	} else {
		log.Printf("SQS_QUEUE_URL not set — running cursor-only mode (new submissions will be delayed until cursor reaches them)")
	}

	for {
		// 1. Try SQS first — instant processing of new submissions.
		if sqsClient != nil {
			body, receipt, err := pollSQS(ctx, sqsClient, queueURL, pollWait)
			if err != nil {
				log.Printf("[runner] SQS receive error: %v", err)
				time.Sleep(time.Duration(shortIdle) * time.Second)
				continue
			}
			if body != "" {
				labelID, ok := extractSNSLabelID(body)
				if !ok {
					log.Printf("[runner] could not parse SQS body, dropping: %.200s", body)
					_, _ = sqsClient.DeleteMessageWithContext(ctx, &sqs.DeleteMessageInput{
						QueueUrl: aws.String(queueURL), ReceiptHandle: aws.String(receipt),
					})
					continue
				}
				evt := events.SNSEvent{Records: []events.SNSEventRecord{{
					SNS: events.SNSEntity{Message: labelID},
				}}}
				log.Printf("[runner] processing SQS-delivered label labelId=%s", labelID)
				if err := Handler(ctx, evt); err != nil {
					log.Printf("[runner] Handler failed for labelId=%s: %v — leaving SQS message for retry", labelID, err)
				} else {
					_, _ = sqsClient.DeleteMessageWithContext(ctx, &sqs.DeleteMessageInput{
						QueueUrl: aws.String(queueURL), ReceiptHandle: aws.String(receipt),
					})
				}
				time.Sleep(time.Duration(minInterval) * time.Second)
				continue
			}
		}

		// 2. Fallback — cursor sweep (catches labels added during downtime).
		if err := Handler(ctx, events.SNSEvent{}); err != nil {
			log.Printf("[runner] cursor-mode error: %v", err)
		}
		time.Sleep(time.Duration(shortIdle) * time.Second)
	}
}

// pollSQS does a single long-poll receive. Returns (body, receipt, err) or
// ("", "", nil) when the queue had no messages within the wait window.
func pollSQS(ctx context.Context, c *sqs.SQS, queueURL string, waitSeconds int) (string, string, error) {
	out, err := c.ReceiveMessageWithContext(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(queueURL),
		MaxNumberOfMessages: aws.Int64(1),
		WaitTimeSeconds:     aws.Int64(int64(waitSeconds)),
	})
	if err != nil {
		return "", "", err
	}
	if len(out.Messages) == 0 {
		return "", "", nil
	}
	return aws.StringValue(out.Messages[0].Body), aws.StringValue(out.Messages[0].ReceiptHandle), nil
}

// extractSNSLabelID handles both SNS-wrapped messages (when SQS subscribes to
// SNS) and bare label-id payloads (when published directly to the queue, e.g.
// via the Lambda from-github writer). The from-github code publishes to SNS
// which fans out to SQS; the SQS body is then a JSON envelope with .Message
// being the actual label id.
func extractSNSLabelID(body string) (string, bool) {
	var env struct {
		Type    string `json:"Type"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal([]byte(body), &env); err == nil && env.Type == "Notification" && env.Message != "" {
		return env.Message, true
	}
	// Fall back: the body might already be just the bare id.
	if _, err := strconv.Atoi(body); err == nil {
		return body, true
	}
	return "", false
}

func getEnvIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func main() {
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		lambda.Start(Handler)
		return
	}
	if os.Getenv("SQS_QUEUE_URL") != "" || os.Getenv("MIN_INTERVAL") != "" {
		// k3s pod mode — long-running SQS-first loop.
		runK3sLoop(context.Background())
		return
	}
	// Local one-shot run for ad-hoc testing.
	if err := Handler(context.TODO(), events.SNSEvent{}); err != nil {
		fmt.Println(err.Error())
	}
}
