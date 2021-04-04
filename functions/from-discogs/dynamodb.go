package main

import (
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/irlndts/go-discogs"
	"github.com/pkg/errors"
	"strconv"
)

const (
	maxBatchSize = 25
)

type Track struct {
	discogs.Track
	LabelId        int                    `json:"dg_label_id"`
	TrackComposite string                 `json:"dg_track_composite"`
	Artists        []discogs.ArtistSource `json:"release_artists"`
	ExtraArtists   []discogs.ArtistSource `json:"release_extraartists"`
	ArtistsSort    string                 `json:"release_artistssort"`
}

func (client *App) AddTracks(release discogs.Release, masterReleaseID int, label int) error {
	for _, chunk := range chunkSlice(release.Tracklist, maxBatchSize) {
		input := &dynamodb.BatchWriteItemInput{}
		writeRequests, err := composeBatchInputs(chunk, masterReleaseID, client.DynamoDBTracksTable, release, label)
		if err != nil {
			return err
		}
		input.SetRequestItems(writeRequests)

		res, err := client.DynamoDB.BatchWriteItem(input)
		if err != nil {
			return errors.Wrap(err, "failed to batch write item tracks")
		}
		if len(res.UnprocessedItems) > 0 {
			return fmt.Errorf("unprocessed items")
		}
	}
	return nil
}

func (client *App) isMasterReleaseAlreadyStored(labelId, masterReleaseId int) (bool, error) {
	queryInput := &dynamodb.QueryInput{
		KeyConditions: map[string]*dynamodb.Condition{
			"dg_label_id": {
				ComparisonOperator: aws.String("EQ"),
				AttributeValueList: []*dynamodb.AttributeValue{
					{
						N: aws.String(strconv.Itoa(labelId)),
					},
				},
			},
			"dg_track_composite": {
				ComparisonOperator: aws.String("BEGINS_WITH"),
				AttributeValueList: []*dynamodb.AttributeValue{
					{
						S: aws.String(strconv.Itoa(masterReleaseId)),
					},
				},
			},
		},
		Limit:     aws.Int64(1),
		TableName: aws.String(client.DynamoDBTracksTable),
	}

	result, err := client.DynamoDB.Query(queryInput)
	if err != nil {
		return false, errors.Wrap(err, "failed to query master release")
	}

	return *result.Count > int64(0), err
}

func (client *App) GetCursor(cursor string) (int, error) {
	resp, err := client.DynamoDB.GetItem(&dynamodb.GetItemInput{
		TableName: &client.DynamoDBCursorTable,
		Key: map[string]*dynamodb.AttributeValue{
			"name": {
				S: aws.String(cursor),
			},
		},
		AttributesToGet: []*string{
			aws.String("value"),
		},
	})
	if err != nil {
		return 0, err
	}

	val, ok := resp.Item["value"]
	if !ok {
		return 0, nil
	}

	return strconv.Atoi(*val.N)
}

func (client *App) SaveCursor(cursor string, value int) error {
	if _, err := client.DynamoDB.PutItem(&dynamodb.PutItemInput{
		TableName: &client.DynamoDBCursorTable,
		Item: map[string]*dynamodb.AttributeValue{
			"name": {
				S: aws.String(cursor),
			},
			"value": {
				N: aws.String(strconv.Itoa(value)),
			},
		},
	}); err != nil {
		return errors.Wrap(err, fmt.Sprintf("failed to save %s cursor", cursor))
	}
	fmt.Printf("successfully set cursor to %d\n", value)

	return nil
}

func composeBatchInputs(tracks []discogs.Track, masterReleaseID int, name string, release discogs.Release, label int) (map[string][]*dynamodb.WriteRequest, error) {
	var wrArr []*dynamodb.WriteRequest

	for i, track := range tracks {
		av, err := dynamodbattribute.MarshalMap(Track{
			track,
			label,
			fmt.Sprintf("%d-%03d", masterReleaseID, i),
			release.Artists,
			release.ExtraArtists,
			release.ArtistsSort,
		})
		if err != nil {
			return nil, errors.Wrap(err, "failed to marshal track as batch inputs")
		}

		pr := dynamodb.PutRequest{}
		pr.SetItem(av)
		wr := dynamodb.WriteRequest{}
		wr.SetPutRequest(&pr)

		wrArr = append(wrArr, &wr)
	}
	wrMap := make(map[string][]*dynamodb.WriteRequest, 1)
	wrMap[name] = wrArr
	return wrMap, nil
}

func chunkSlice(slice []discogs.Track, chunkSize int) [][]discogs.Track {
	var chunks [][]discogs.Track
	for i := 0; i < len(slice); i += chunkSize {
		end := i + chunkSize

		// necessary check to avoid slicing beyond
		// slice capacity
		if end > len(slice) {
			end = len(slice)
		}

		chunks = append(chunks, slice[i:end])
	}

	return chunks
}
