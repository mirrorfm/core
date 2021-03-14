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
)

type retryableDiscogsClient struct {
	Discogs                       discogs.Discogs
	DynamoDB                      *dynamodb.DynamoDB
	DynamoDBTracksTable           string
	backoff                       backoff.BackOff
	highestReleaseID              int
	uniqueMasterReleases          []int
	uniqueMasterReleasesExistence map[int][]discogs.Track
}

func getApp() (retryableDiscogsClient, error) {
	// MySQL
	dbHost := os.Getenv("DB_HOST")
	dbUser := os.Getenv("DB_USERNAME")
	dbPass := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	_, err := sql.Open("mysql", dbUser+":"+dbPass+"@tcp("+dbHost+")/"+dbName+"?parseTime=true")
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
		UserAgent: "Some Name",
	})

	return retryableDiscogsClient{
		discogsClient,
		dynamoClient,
		"mirrorfm_dg_tracks",
		backoff.WithMaxRetries(backoff.NewExponentialBackOff(),	100),
		12893584, // TODO get from DB
		nil,
		map[int][]discogs.Track{},
	}, nil
}

func Handler(ctx context.Context) error {
	client, err := getApp()
	if err != nil {
		return err
	}

	// TODO Insert in Label table
	maxPages := 1
	page := 1
	label := 77423

	for page <= maxPages {
		releases, err := client.GetLabelReleases(page, label)
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

	for _, release := range client.uniqueMasterReleases {
		// TODO ignore if release already in DB
		fmt.Printf("tracks in %d %d\n", release, len(client.uniqueMasterReleasesExistence[release]))
		err := client.addTracks(client.uniqueMasterReleasesExistence[release], release, label)
		if err != nil {
			return err
		}
	}

	// TODO set new highest release ID
	return nil
}

func (client *retryableDiscogsClient) populateUniqueMasterReleases(releases *discogs.LabelReleases) error {
	for _, release := range releases.Releases {
		id := release.ID
		if id <= client.highestReleaseID {
			continue
		}

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
			client.uniqueMasterReleasesExistence[masterID] = resp.Tracklist
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