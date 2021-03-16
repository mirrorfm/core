package main

import (
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/irlndts/go-discogs"
	"strconv"
)

const (
	maxBatchSize = 25
)

type Track struct {
	discogs.Track
	LabelId 		int 					`json:"dg_label_id"`
	TrackComposite 	string 					`json:"dg_track_composite"`
	Artists 		[]discogs.ArtistSource 	`json:"artists_source"`
	ArtistsSort 	string 					`json:"artists_sort"`
}

func (client *retryableDiscogsClient) addTracks(release discogs.Release, masterReleaseID int, label int) error {
	for _, chunk := range chunkSlice(release.Tracklist, maxBatchSize) {
		input := &dynamodb.BatchWriteItemInput{}
		writeRequests, err := composeBatchInputs(chunk, masterReleaseID, client.DynamoDBTracksTable, release, label)
		if err != nil {
			return err
		}
		input.SetRequestItems(writeRequests)

		res, err := client.DynamoDB.BatchWriteItem(input)
		if err != nil {
			return err
		}
		if len(res.UnprocessedItems) > 0 {
			return fmt.Errorf("unprocessed items")
		}
	}
	return nil
}

func composeBatchInputs(tracks []discogs.Track, masterReleaseID int, name string, release discogs.Release, label int) (map[string][]*dynamodb.WriteRequest, error) {
	var wrArr []*dynamodb.WriteRequest

	for i, track := range tracks {
		av, err := dynamodbattribute.MarshalMap(Track{
			track,
			label,
			fmt.Sprintf("%d-%d", masterReleaseID, i),
			release.Artists,
			release.ArtistsSort,
		})
		if err != nil {
			return nil, err
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

func (client *retryableDiscogsClient) masterReleaseAlreadyStored(labelId, masterReleaseId int) (bool, error) {
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
		Limit:            aws.Int64(1),
		TableName:        aws.String(client.DynamoDBTracksTable),
	}

	result, err := client.DynamoDB.Query(queryInput)
	if err != nil {
		return false, err
	}

	return *result.Count > int64(0), err
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