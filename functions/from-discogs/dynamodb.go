package main

import (
	"fmt"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/irlndts/go-discogs"
)

const (
	maxBatchSize = 25
)

type Track struct {
	discogs.Track
	LabelId int `json:"dg_label_id"`
	TrackComposite string `json:"dg_track_composite"`
}

func (client *retryableDiscogsClient) addTracks(tracks []discogs.Track, release int, label int) error {
	for _, chunk := range chunkSlice(tracks, maxBatchSize) {
		input := &dynamodb.BatchWriteItemInput{}
		writeRequests, err := composeBatchInputs(chunk, release, client.DynamoDBTracksTable, label)
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

func composeBatchInputs(tracks []discogs.Track, release int, name string, label int) (map[string][]*dynamodb.WriteRequest, error) {
	var wrArr []*dynamodb.WriteRequest

	for i, track := range tracks {
		av, err := dynamodbattribute.MarshalMap(Track{
			track,
			label,
			fmt.Sprintf("%d-%s-%s", release, fmt.Sprintf("%03d", i), track.Title),
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