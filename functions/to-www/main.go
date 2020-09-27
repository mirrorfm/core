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
	DynamoDB                     *dynamodb.DynamoDB
	DynamoDBEventsTable          string
	DynamoDBTracksTable          string
	DynamoDBDuplicateTracksTable string
	SQLDriver                    *sql.DB
}

var ginLambda *ginadapter.GinLambda

func init() {
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
		events, _ := client.getEvents(6)
		handleAPIError(c, err)

		c.JSON(200, events)
	})

	r.GET("/channels", func(c *gin.Context) {
		totalTracks, err := client.getTableCount(client.DynamoDBTracksTable)
		handleAPIError(c, err)
		foundTracks, err := client.getTableCount(client.DynamoDBDuplicateTracksTable)
		handleAPIError(c, err)
		channels, err := client.getYoutubeChannels("c.id", "ASC", 500, 4)
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

	r.GET("/home", func(c *gin.Context) {
		var limitChannels = 6
		var limitGenres = 4

		lastUpdated, err := client.getYoutubeChannels("last_found_time", "DESC", limitChannels, limitGenres)
		handleAPIError(c, err)
		mostFollowed, err := client.getYoutubeChannels("count_followers", "DESC", limitChannels, limitGenres)
		handleAPIError(c, err)
		mostUploads, err := client.getYoutubeChannels("count_tracks", "DESC", limitChannels, limitGenres)
		handleAPIError(c, err)
		recentlyAdded, err := client.getYoutubeChannels("id", "DESC", limitChannels, limitGenres)
		handleAPIError(c, err)

		c.JSON(200, gin.H{
			"lastUpdated": lastUpdated,
			"mostFollowed": mostFollowed,
			"mostUploads": mostUploads,
			"recentlyAdded": recentlyAdded,
		})
	})

	if os.Getenv("AWS_EXECUTION_ENV") == "" {
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
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Credentials", "true")
	c.Header("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
	c.Header("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")
}

func handleAPIError(c *gin.Context, err error) {
	if err != nil {
		c.JSON(500, gin.H{
			"error": err.Error(),
		})
	}
}

type Genre struct {
	Name  string `json:"name" dynamodbav:"genre_name"`
	Count int `json:"count"`
}

type YoutubeChannel struct {
	ID                 int       `json:"id"`
	ChannelId          string    `json:"channel_id"`
	ChannelName        string    `json:"channel_name"`
	CountTracks        int       `json:"count_tracks"`
	FoundTracks        int       `json:"found_tracks"`
	LastUploadDatetime time.Time `json:"last_upload_datetime"`
	ThumbnailDefault   string    `json:"thumbnail_default"`
	UploadPlaylistId   string    `json:"upload_playlist_id"`
	PlaylistId         string    `json:"playlist_id"`
	CountFollowers     int       `json:"count_followers"`
	Genres             []Genre   `json:"genres"`
	LastFoundTime	   time.Time `json:"last_found_time"`
}

type Event struct {
	Host            string 			`json:"host"`
	Timestamp       string 			`json:"timestamp"`
	Added           string 			`json:"added"`
	ChannelID       string 			`json:"channel_id" dynamodbav:"channel_id"`
	SpotifyPlaylist string 			`json:"spotify_playlist" dynamodbav:"spotify_playlist"`
	Channel         YoutubeChannel 	`json:"channel,omitempty"`
}

func (c *Client) getYoutubeChannels(orderBy, order string, limit, limitGenres int) (res []YoutubeChannel, err error) {
	query := fmt.Sprintf(`
		SELECT
			c.id, c.channel_id, c.channel_name, c.count_tracks as count_tracks,
			c.last_upload_datetime, c.thumbnail_default, c.upload_playlist_id,
			p.spotify_playlist, p.found_tracks, p.count_followers as count_followers,
			p.last_found_time as last_found_time
		FROM
			yt_channels as c
		INNER JOIN
			yt_playlists p on c.channel_id = p.channel_id
		GROUP BY id ORDER BY %s %s LIMIT ?`, orderBy, order)
	selDB, err := c.SQLDriver.Query(query, limit)
	if err != nil {
		fmt.Println(err.Error())
		return res, err
	}

	var ch YoutubeChannel
	for selDB.Next() {
		err = selDB.Scan(
			&ch.ID,
			&ch.ChannelId,
			&ch.ChannelName,
			&ch.CountTracks,
			&ch.LastUploadDatetime,
			&ch.ThumbnailDefault,
			&ch.UploadPlaylistId,
			&ch.PlaylistId,
			&ch.FoundTracks,
			&ch.CountFollowers,
			&ch.LastFoundTime)
		if err != nil {
			return res, err
		}
		ch, err := c.populateWithGenres(ch, limitGenres)
		if err != nil {
			return res, err
		}
		res = append(res, ch)
	}
	return res, nil
}

func (c *Client) populateWithGenres(channel YoutubeChannel, limitGenres int) (YoutubeChannel, error) {
	query := `
		SELECT genre_name, count
		FROM yt_genres
		WHERE yt_channel_id = ?
		ORDER BY count DESC
		LIMIT ?`
	db, err := c.SQLDriver.Query(query, &channel.ID, limitGenres)
	if err != nil {
		fmt.Println(err.Error())
		return channel, err
	}
	var g Genre
	var genres []Genre
	for db.Next() {
		err = db.Scan(&g.Name, &g.Count)
		if err != nil {
			return channel, err
		}
		genres = append(genres, g)
	}
	channel.Genres = genres
	return channel, nil
}

func (c *Client) getTableCount(table string) (count *int64, err error) {
	describeTable := &dynamodb.DescribeTableInput{
		TableName: aws.String(table),
	}
	res, err := c.DynamoDB.DescribeTable(describeTable)
	if err != nil {
		return nil, err
	}
	return res.Table.ItemCount, nil
}

func (c *Client) getYoutubeChannel(channelId string) (ch YoutubeChannel, err error) {
	selDB := c.SQLDriver.QueryRow(`
		SELECT
		    c.id, c.channel_id, c.channel_name, c.count_tracks, c.last_upload_datetime,
		    c.thumbnail_default, c.upload_playlist_id, p.spotify_playlist, p.found_tracks
		FROM yt_channels as c
		INNER JOIN yt_playlists p on c.channel_id = p.channel_id
		WHERE c.channel_id = ?`, channelId)

	err = selDB.Scan(
		&ch.ID,
		&ch.ChannelId,
		&ch.ChannelName,
		&ch.CountTracks,
		&ch.LastUploadDatetime,
		&ch.ThumbnailDefault,
		&ch.UploadPlaylistId,
		&ch.PlaylistId,
		&ch.FoundTracks)

	return ch, err
}

func (c *Client) getEvents(count int) (events []Event, err error) {
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
		Limit:            aws.Int64(int64(count)),
		ScanIndexForward: aws.Bool(false),
		TableName:        aws.String(c.DynamoDBEventsTable),
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
