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
	"github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
	"log"
	"os"
)

type Client struct {
	DynamoDB                     *dynamodb.DynamoDB
	DynamoDBEventsTable          string
	DynamoDBTracksTable          string
	DynamoDBDuplicateTracksTable string
	SQLDriver                    *sql.DB
}

const (
	entityLimit            = 500
	genreLimit             = 4
	homeChannelsLimit      = 6
	homeChannelsGenreLimit = 20
)

var isLocal bool
var ginLambda *ginadapter.GinLambda

func init() {
	isLocal = os.Getenv("AWS_EXECUTION_ENV") == ""

	// stdout and stderr are sent to AWS CloudWatch Logs
	log.Printf("Gin cold start")
	r := gin.Default()
	r.Use(cors)

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
		DynamoDB:                     dynamoClient,
		DynamoDBEventsTable:          "mirrorfm_events",
		DynamoDBTracksTable:          "mirrorfm_yt_tracks",
		DynamoDBDuplicateTracksTable: "mirrorfm_yt_duplicates",
		SQLDriver:                    sqlDriver,
	}

	if err != nil {
		panic(err.Error())
	}

	r.GET("/events", func(c *gin.Context) {
		lastEvents, _ := client.getEvents(100)
		handleAPIError(c, err)

		c.JSON(200, lastEvents)
	})

	r.GET("/channels", func(c *gin.Context) {
		totalTracks, err := client.getTableCount(client.DynamoDBTracksTable)
		handleAPIError(c, err)
		foundTracks, err := client.getTableCount(client.DynamoDBDuplicateTracksTable)
		handleAPIError(c, err)
		channels, err := client.getYoutubeChannels("c.id", "ASC", entityLimit, genreLimit)
		handleAPIError(c, err)

		c.JSON(200, gin.H{
			"youtube":        channels,
			"total_channels": len(channels),
			"total_tracks":   totalTracks,
			"found_tracks":   foundTracks,
		})
	})

	r.GET("/channels/:id", func(c *gin.Context) {
		channel, err := client.getYoutubeChannel(c.Param("id"))
		handleAPIError(c, err)

		c.JSON(200, gin.H{
			"channel": channel,
		})
	})

	r.GET("/labels", func(c *gin.Context) {
		totalTracks, err := client.getTableCount(client.DynamoDBTracksTable)
		handleAPIError(c, err)
		foundTracks, err := client.getTableCount(client.DynamoDBDuplicateTracksTable)
		handleAPIError(c, err)
		labels, err := client.getDiscogsLabels("l.id", "ASC", entityLimit, genreLimit)
		handleAPIError(c, err)

		c.JSON(200, gin.H{
			"discogs":      labels,
			"total_labels": len(labels),
			"total_tracks": totalTracks,
			"found_tracks": foundTracks,
		})
	})

	r.GET("/labels/:id", func(c *gin.Context) {
		label, err := client.getDiscogsLabel(c.Param("id"))
		handleAPIError(c, err)

		c.JSON(200, gin.H{
			"label": label,
		})
	})

	r.GET("/home", func(c *gin.Context) {
		lastUpdated, err := client.getYoutubeChannels("last_found_time", "DESC", homeChannelsLimit, homeChannelsGenreLimit)
		handleAPIError(c, err)
		mostFollowed, err := client.getYoutubeChannels("count_followers", "DESC", homeChannelsLimit, homeChannelsGenreLimit)
		handleAPIError(c, err)
		mostUploads, err := client.getYoutubeChannels("count_tracks", "DESC", homeChannelsLimit, homeChannelsGenreLimit)
		handleAPIError(c, err)
		recentlyAdded, err := client.getYoutubeChannels("id", "DESC", homeChannelsLimit, homeChannelsGenreLimit)
		handleAPIError(c, err)
		lastTerminated, err := client.getYoutubeChannelsTerminated("terminated_datetime", "DESC", homeChannelsLimit, homeChannelsGenreLimit)
		handleAPIError(c, err)
		rarestUploads, err := client.getYoutubeChannels("(found_tracks*100/count_tracks)", "ASC", homeChannelsLimit, homeChannelsGenreLimit)
		handleAPIError(c, err)

		c.JSON(200, gin.H{
			"lastUpdated":    lastUpdated,
			"mostFollowed":   mostFollowed,
			"mostUploads":    mostUploads,
			"recentlyAdded":  recentlyAdded,
			"lastTerminated": lastTerminated,
			"rarestUploads":  rarestUploads,
		})
	})

	if isLocal {
		fmt.Println("Running in development mode")
		_ = r.Run()
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

func cors(c *gin.Context) {
	c.Header("Content-Type", "application/json")
	c.Header("Access-Control-Allow-Credentials", "true")
	c.Header("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
	c.Header("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")

	if isLocal {
		c.Header("Access-Control-Allow-Origin", "*")
	} else {
		c.Header("Access-Control-Allow-Origin", "https://mirror.fm")
	}
}

func handleAPIError(c *gin.Context, err error) {
	if err != nil {
		c.JSON(500, gin.H{
			"error": err.Error(),
		})
	}
}
