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
	"log"
	"os"
	"time"
)

type retryableDiscogsClient struct {
	Discogs                       discogs.Discogs
	DynamoDB                      *dynamodb.DynamoDB
	DynamoDBTracksTable           string
	SQLDriver					  *sql.DB
	backoff                       backoff.BackOff
	highestReleaseID              int
	uniqueMasterReleases          []int
	uniqueMasterReleasesExistence map[int]discogs.Release
	labelReleases                 int
	labelTracks                   int
}

func getApp() (retryableDiscogsClient, error) {
	// MySQL
	dbHost := os.Getenv("DB_HOST")
	dbUser := os.Getenv("DB_USERNAME")
	dbPass := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	sqlDriver, err := sql.Open("mysql", dbUser+":"+dbPass+"@tcp("+dbHost+")/"+dbName+"?parseTime=true")
	if err != nil {
		return retryableDiscogsClient{}, err
	}

	// DynamoDB
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))
	dynamoClient := dynamodb.New(sess, &aws.Config{
		Region: aws.String("eu-west-1"),
	})

	discogsClient, err := discogs.New(&discogs.Options{
		UserAgent: "Mirror.FM",
	})
	if err != nil {
		return retryableDiscogsClient{}, err
	}

	return retryableDiscogsClient{
		Discogs: discogsClient,
		DynamoDB: dynamoClient,
		DynamoDBTracksTable: "mirrorfm_dg_tracks",
		SQLDriver: sqlDriver,
		backoff: backoff.WithMaxRetries(backoff.NewExponentialBackOff(),100),
		uniqueMasterReleases: nil,
		uniqueMasterReleasesExistence: map[int]discogs.Release{},
	}, nil
}

type Label struct {
	HighestReleaseID int `json:"highest_dg_release"`
	LabelReleases    int `json:"label_releases"`
	LabelTracks      int `json:"label_tracks"`
}

func Handler(ctx context.Context) error {
	client, err := getApp()
	if err != nil {
		return err
	}

	labelId := 77423

	label, err := client.GetLabel(labelId)
	if err != nil {
		return err
	}

	fmt.Printf("%+v\n", label)
	_, err = client.SQLDriver.Exec(fmt.Sprintf(`
		UPDATE dg_labels
		SET
			label_name = ?,
			added_datetime = ?
		WHERE label_id = ?
	`), label.Name, time.Now(), label.ID)
	if err != nil {
		return err
	}

	l := Label{}
	selDB := client.SQLDriver.QueryRow(fmt.Sprintf(`
		SELECT
			highest_dg_release,
			label_releases,
			label_tracks
		FROM dg_labels
		WHERE label_id = ?
	`), label.ID)
	err = selDB.Scan(
		&l.HighestReleaseID,
		&l.LabelReleases,
		&l.LabelTracks,
	)
	if err != nil {
		return err
	}
	client.highestReleaseID = l.HighestReleaseID
	client.labelReleases = l.LabelReleases
	client.labelTracks = l.LabelTracks

	maxPages := 1
	page := 1
	for page <= maxPages {
		releases, err := client.GetLabelReleases(page, labelId)
		if err != nil {
			return err
		}
		maxPages = releases.Pagination.Pages
		log.Printf("Page %d/%d\n", page, maxPages)
		page += 1

		err = client.populateUniqueMasterReleases(releases)
		if err != nil {
			return err
		}
	}

	for _, masterReleaseId := range client.uniqueMasterReleases {
		alreadyStored, err := client.masterReleaseAlreadyStored(labelId, masterReleaseId)
		if err != nil {
			return err
		}
		if alreadyStored {
			fmt.Println("Already stored")
			continue
		}

		fmt.Printf("tracks in %d %d\n", masterReleaseId, len(client.uniqueMasterReleasesExistence[masterReleaseId].Tracklist))
		err = client.addTracks(client.uniqueMasterReleasesExistence[masterReleaseId], masterReleaseId, labelId)
		if err != nil {
			return err
		}

		client.labelReleases += 1
		client.labelTracks += len(client.uniqueMasterReleasesExistence[masterReleaseId].Tracklist)
	}

	_, err = client.SQLDriver.Exec(fmt.Sprintf(`
		UPDATE dg_labels
		SET
			highest_dg_release = ?,
			label_releases = ?,
			label_tracks = ?
		WHERE label_id = ?;
	`), client.highestReleaseID, client.labelReleases, client.labelTracks, label.ID)
	if err != nil {
		return err
	}

	return nil
}

func (client *retryableDiscogsClient) populateUniqueMasterReleases(releases *discogs.LabelReleases) error {
	for _, release := range releases.Releases {
		id := release.ID
		if id <= client.highestReleaseID {
			continue
		}
		client.highestReleaseID = id

		resp, err := client.GetRelease(id)
		if err != nil {
			return err
		}

		masterID := resp.MasterID
		if masterID == 0 {
			masterID = release.ID
		}

		if _, ok := client.uniqueMasterReleasesExistence[masterID]; !ok {
			client.uniqueMasterReleases = append(client.uniqueMasterReleases, masterID)
			client.uniqueMasterReleasesExistence[masterID] = *resp
			log.Printf("%d => %d\n", id, masterID)
		}
	}
	return nil
}

func main() {
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		lambda.Start(Handler)
	} else {
		err := Handler(context.TODO())
		if err != nil {
			panic(err.Error())
		}
	}
}