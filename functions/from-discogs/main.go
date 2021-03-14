package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/cenkalti/backoff/v4"
	_ "github.com/go-sql-driver/mysql"
	"github.com/irlndts/go-discogs"
	"log"
	"os"
)

type retryableDiscogsClient struct {
	discogs.Discogs
	backoff                       backoff.BackOff
	highestReleaseID              int
	uniqueMasterReleases          []int
	uniqueMasterReleasesExistence map[int][]discogs.Track
}

func init() {
	// MySQL
	dbHost := os.Getenv("DB_HOST")
	dbUser := os.Getenv("DB_USERNAME")
	dbPass := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	_, err := sql.Open("mysql", dbUser+":"+dbPass+"@tcp("+dbHost+")/"+dbName+"?parseTime=true")

	if err != nil {
		panic(err.Error())
	}
	discogsClient, err := discogs.New(&discogs.Options{
		UserAgent: "Some Name",
	})
	client := retryableDiscogsClient{
		discogsClient,
		backoff.WithMaxRetries(backoff.NewExponentialBackOff(),	100),
		12893584,
		nil,
		map[int][]discogs.Track{},
	}
	if err != nil {
		panic(err.Error())
	}
	var releases *discogs.LabelReleases

	// TODO Insert in Label table

	maxPages := 1
	page := 1

	for page <= maxPages {
		if releases, err = client.GetLabelReleases(page); err != nil {
			panic(err.Error())
		}
		maxPages = releases.Pagination.Pages
		log.Printf("Page %d/%d\n", page, maxPages)
		page += 1

		err := client.populateUniqueMasterReleases(releases)
		if err != nil {
			panic(err.Error())
		}
	}

	for _, release := range client.uniqueMasterReleases {
		// TODO ignore if release already in DB
		fmt.Printf("tracks in %d %d\n", release, len(client.uniqueMasterReleasesExistence[release]))
		// TODO send tracks to SNS
		// TODO insert in DB
	}
}

func (r *retryableDiscogsClient) populateUniqueMasterReleases(releases *discogs.LabelReleases) error {
	for _, release := range releases.Releases {
		id := release.ID
		if id <= r.highestReleaseID {
			continue
		}

		resp, err := r.GetRelease(id)
		if err != nil {
			return err
		}

		masterID := resp.MasterID
		if masterID == 0 {
			masterID = release.ID
		}

		if _, ok := r.uniqueMasterReleasesExistence[masterID]; !ok {
			r.uniqueMasterReleases = append(r.uniqueMasterReleases, masterID)
			r.uniqueMasterReleasesExistence[masterID] = resp.Tracklist
			log.Printf("%d => %d\n", id, masterID)
		}
	}
	return nil
}

func (r *retryableDiscogsClient) GetRelease(id int) (resp *discogs.Release, err error) {
	err = backoff.Retry(func() error {
		resp, err = r.Discogs.Release(id)
		if err != nil {
			return err
		}
		// Success, don't retry
		return nil
	}, r.backoff)
	r.backoff.Reset()
	return
}

func (r *retryableDiscogsClient) GetLabelReleases(page int) (resp *discogs.LabelReleases, err error) {
	err = backoff.Retry(func() error {
		resp, err = r.Discogs.LabelReleases(77423, &discogs.Pagination{
			Sort: "year",
			SortOrder: "desc",
			PerPage: 100,
			Page: page,
		})
		if err != nil {
			return err
		}
		// Success, don't retry
		return nil
	}, r.backoff)
	r.backoff.Reset()
	return
}

func Handler(ctx context.Context, snsEvent events.SNSEvent) {
	for _, record := range snsEvent.Records {
		snsRecord := record.SNS

		fmt.Printf("[%s %s] Message = %s \n", record.EventSource, snsRecord.Timestamp, snsRecord.Message)
	}
}

func main() {
	lambda.Start(Handler)
}