package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	_ "github.com/go-sql-driver/mysql"
	"log"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/gin-gonic/gin"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
)

type Client struct {
	DynamoDB      		*dynamodb.DynamoDB
	DynamoDBEventsTable string
	SQLDriver			*sql.DB
}

var ginLambda *ginadapter.GinLambda

func init() {
	// stdout and stderr are sent to AWS CloudWatch Logs
	log.Printf("Gin cold start")
	r := gin.Default()

	// DynamoDB
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))
	dynamoClient := dynamodb.New(sess, &aws.Config{
		Region: aws.String("eu-west-1"),
	})

	// MySQL
	dbHost := os.Getenv("DB_HOST")
	dbUser := os.Getenv("DB_USERNAME")
	dbPass := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	sqlDriver, err := sql.Open("mysql", dbUser+":"+dbPass+"@tcp("+dbHost+")/"+dbName+"?parseTime=true")

	client := Client{
		DynamoDB:      			dynamoClient,
		DynamoDBEventsTable: 	"mirrorfm_events",
		SQLDriver: 	 			sqlDriver,
	}

	if err != nil {
		panic(err.Error())
	}

	r.GET("/events", func(c *gin.Context) {
		events, _ := client.getEvents()
		c.JSON(200, events)
	})

	r.GET("/home", func(c *gin.Context) {
		channels, _ := client.getChannels()
		c.JSON(200, channels)
	})

	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "yo",
		})
	})

	if true {
		fmt.Println("Running in development mode")
		r.Run()
	} else {
		fmt.Println("Running in lambda mode")
		ginLambda = ginadapter.New(r)
	}

}

func Handler(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	// If no name is provided in the HTTP request body, throw an error
	return ginLambda.ProxyWithContext(ctx, req)
}

func main() {
	lambda.Start(Handler)
}

type YoutubeChannel struct {
	Id    				int
	ChannelId  			string
	ChannelName 		string
	CountTracks 		int
	LastUploadDatetime 	time.Time
	ThumbnailDefault	string
	UploadPlaylistId	string
}

type Event struct {
	Host 			string
	Timestamp 		string
	Added 			string
	ChannelID 		string `dynamodbav:"channel_id"`
	SpotifyPlaylist string `dynamodbav:"spotify_playlist"`
}

func (c *Client) getChannels() (res []YoutubeChannel, err error) {
	selDB, err := c.SQLDriver.Query("SELECT id, channel_id, channel_name, count_tracks," +
		"last_upload_datetime, thumbnail_default, upload_playlist_id FROM yt_channels")
	if err != nil {
		return res, err
	}

	var ch YoutubeChannel
	for selDB.Next() {
		err = selDB.Scan(
			&ch.Id,
			&ch.ChannelId,
			&ch.ChannelName,
			&ch.CountTracks,
			&ch.LastUploadDatetime,
			&ch.ThumbnailDefault,
			&ch.UploadPlaylistId)
		if err != nil {
			return res, err
		}
		res = append(res, ch)
	}
	defer c.SQLDriver.Close()
	return res, nil
}

func (c *Client) getEvents() (events []Event, err error) {
	queryInput := &dynamodb.QueryInput{
		KeyConditions: map[string]*dynamodb.Condition{
			"host": {
				ComparisonOperator: aws.String("EQ"),
				AttributeValueList: []*dynamodb.AttributeValue{
					{
						S: aws.String("yt"),
					},
				},
			},
		},
		Limit:      		aws.Int64(40),
		ScanIndexForward:   aws.Bool(false),
		TableName:         	aws.String(c.DynamoDBEventsTable),
	}

	result, err := c.DynamoDB.Query(queryInput)
	if err != nil {
		fmt.Println("Query API call failed:")
		fmt.Println(err.Error())
		return events, err
	}

	err = dynamodbattribute.UnmarshalListOfMaps(result.Items, &events)
	if err != nil {
		fmt.Println("Got error unmarshalling events")
		fmt.Println(err.Error())
		return events, err
	}

	return events, nil
}