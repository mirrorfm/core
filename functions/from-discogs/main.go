package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/cenkalti/backoff/v4"
	_ "github.com/go-sql-driver/mysql"
	"github.com/irlndts/go-discogs"
	"github.com/pkg/errors"
	"strconv"

	//"github.com/disintegration/imaging"
	"log"
	"os"
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

	label, err := app.GetLabel(labelId)
	if err != nil {
		return errors.Wrap(err, "failed to retrieve label")
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
		if err != nil {
			return errors.Wrap(err, "failed to get label releases")
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
		if err != nil {
			return uniqueMasterReleases, skipped, err
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

func main() {
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		lambda.Start(Handler)
	} else {
		// Also handle loop
		err := Handler(context.TODO(), events.SNSEvent{
			Records: []events.SNSEventRecord{
				//{
				//	SNS: events.SNSEntity{
				//		Message: "720419", // 77423
				//	},
				//},
			},
		})
		if err != nil {
			fmt.Println(err.Error())
		}
	}
}
