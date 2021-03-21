package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/cenkalti/backoff/v4"
	_ "github.com/go-sql-driver/mysql"
	"github.com/irlndts/go-discogs"
	"github.com/pkg/errors"
	"log"
	"os"
)

type App struct {
	Discogs             discogs.Discogs
	DynamoDB            *dynamodb.DynamoDB
	DynamoDBTracksTable string
	SQLDriver           *sql.DB
	Backoff             backoff.BackOff
}

type LocalLabel struct {
	ID                  int
	HighestReleaseID    int `json:"highest_dg_release"`
	LabelReleases       int `json:"label_releases"`
	LabelTracks         int `json:"label_tracks"`
	MasterReleasesCache map[int]discogs.Release
	LastPage            int `json:"last_page"`
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
	discogsClient, err := discogs.New(&discogs.Options{
		UserAgent: "Mirror.FM",
	})
	if err != nil {
		return App{}, errors.Wrap(err, "failed to set up discogs client")
	}

	return App{
		Discogs:             discogsClient,
		DynamoDB:            dynamoClient,
		DynamoDBTracksTable: "mirrorfm_dg_tracks",
		SQLDriver:           sqlDriver,
		Backoff:             backoff.WithMaxRetries(backoff.NewExponentialBackOff(),100),
	}, nil
}

func Handler(ctx context.Context) error {
	app, err := getApp()
	if err != nil {
		return err
	}

	labelId := 77423

	label, err := app.GetLabel(labelId)
	if err != nil {
		return err
	}

	err = app.UpdateLabelWithAddedDatetime(label)
	if err != nil {
		return err
	}

	localLabel, err := app.GetLabelInfo(label.ID)
	if err != nil {
		return err
	}

	log.Printf("%+v\n", localLabel)

	for {
		fmt.Println("weffwe")
		releases, err := app.GetLabelReleases(localLabel.LastPage, labelId)
		if err != nil {
			return err
		}

		localLabel.MaxPages = releases.Pagination.Pages

		log.Printf("Page %d/%d\n", localLabel.LastPage + 1, localLabel.MaxPages + 1)

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

	return nil
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
		if alreadyStored, err := client.isMasterReleaseAlreadyStored(localLabel.ID, masterReleaseId); err != nil {
			return err
		} else if alreadyStored {
			fmt.Println("Already stored")
			continue
		}

		fmt.Printf("tracks in %d %d\n", masterReleaseId, len(localLabel.MasterReleasesCache[masterReleaseId].Tracklist))
		err := client.AddTracks(localLabel.MasterReleasesCache[masterReleaseId], masterReleaseId, localLabel.ID)
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
	// will have a new/high release ID.
	return localLabel.DidInit.Bool && releaseId <= localLabel.HighestReleaseID
}

func main() {
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		lambda.Start(Handler)
	} else {
		err := Handler(context.TODO())
		if err != nil {
			fmt.Println(err.Error())
		}
	}
}