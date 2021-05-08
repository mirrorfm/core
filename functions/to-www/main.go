package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	_ "github.com/go-sql-driver/mysql"
	"log"
	"os"
	"sort"
	"strconv"
	"sync"
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

const (
	youtubeChannelsLimit      = 500
	youtubeChannelsGenreLimit = 4
	homeChannelsLimit         = 6
	homeChannelsGenreLimit    = 20
)

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
		lastEvents, _ := client.getEvents(100)
		handleAPIError(c, err)

		c.JSON(200, lastEvents)
	})

	r.GET("/channels", func(c *gin.Context) {
		totalTracks, err := client.getTableCount(client.DynamoDBTracksTable)
		handleAPIError(c, err)
		foundTracks, err := client.getTableCount(client.DynamoDBDuplicateTracksTable)
		handleAPIError(c, err)
		channels, err := client.getYoutubeChannels("c.id", "ASC", youtubeChannelsLimit, youtubeChannelsGenreLimit)
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
	Count int    `json:"count"`
}

type YoutubeChannel struct {
	ID                 int          `json:"id"`
	ChannelId          string       `json:"channel_id"`
	ChannelName        string       `json:"channel_name"`
	CountTracks        int          `json:"count_tracks"`
	FoundTracks        int          `json:"found_tracks"`
	LastUploadDatetime time.Time    `json:"last_upload_datetime"`
	ThumbnailMedium    string       `json:"thumbnail_medium"`
	UploadPlaylistId   string       `json:"upload_playlist_id"`
	TerminatedDatetime sql.NullTime `json:"terminated_datetime"`
	AddedDatetime      sql.NullTime `json:"added_datetime"`
	PlaylistId         string       `json:"playlist_id"`
	CountFollowers     int          `json:"count_followers"`
	Genres             []Genre      `json:"genres"`
	LastFoundTime      time.Time    `json:"last_found_time"`
}

type Event struct {
	Host            string `json:"host"`
	Timestamp       string `json:"timestamp"`
	Added           string `json:"added"`
	SpotifyPlaylist string `json:"spotify_playlist" dynamodbav:"spotify_playlist"`
	EntityID   		string `json:"entity_id" dynamodbav:"entity_id"`
	EntityName 		string `json:"entity_name" dynamodbav:"entity_name"`
}

func (c *Client) getYoutubeChannels(orderBy, order string, limit, limitGenres int) (res []YoutubeChannel, err error) {
	query := fmt.Sprintf(`
		SELECT
			c.id, c.channel_id, c.channel_name, c.count_tracks as count_tracks,
			c.last_upload_datetime, c.thumbnail_medium, c.upload_playlist_id,
			c.terminated_datetime, c.added_datetime,
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
			&ch.ThumbnailMedium,
			&ch.UploadPlaylistId,
			&ch.TerminatedDatetime,
			&ch.AddedDatetime,
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

func (c *Client) getYoutubeChannelsTerminated(orderBy, order string, limit, limitGenres int) (res []YoutubeChannel, err error) {
	query := fmt.Sprintf(`
		SELECT
			c.id, c.channel_id, c.channel_name, c.count_tracks as count_tracks,
			c.last_upload_datetime, c.thumbnail_medium, c.upload_playlist_id,
			c.terminated_datetime, c.added_datetime,
			p.spotify_playlist, p.found_tracks, p.count_followers as count_followers,
			p.last_found_time as last_found_time
		FROM
			yt_channels as c
		INNER JOIN
			yt_playlists p on c.channel_id = p.channel_id
		WHERE
			terminated_datetime IS NOT NULL
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
			&ch.ThumbnailMedium,
			&ch.UploadPlaylistId,
			&ch.TerminatedDatetime,
			&ch.AddedDatetime,
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
	query := fmt.Sprintf(`
		SELECT genre_name, count
		FROM yt_genres
		WHERE yt_channel_id = ?
		ORDER BY count DESC
		LIMIT ?`)
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
	selDB := c.SQLDriver.QueryRow(fmt.Sprintf(`
		SELECT
		    c.id, c.channel_id, c.channel_name, c.count_tracks, c.last_upload_datetime,
		    c.thumbnail_medium, c.upload_playlist_id, c.terminated_datetime, c.added_datetime,
		    p.spotify_playlist, p.found_tracks
		FROM yt_channels as c
		INNER JOIN yt_playlists p on c.channel_id = p.channel_id
		WHERE c.channel_id = ?`), channelId)

	err = selDB.Scan(
		&ch.ID,
		&ch.ChannelId,
		&ch.ChannelName,
		&ch.CountTracks,
		&ch.LastUploadDatetime,
		&ch.ThumbnailMedium,
		&ch.UploadPlaylistId,
		&ch.TerminatedDatetime,
		&ch.AddedDatetime,
		&ch.PlaylistId,
		&ch.FoundTracks)

	return ch, err
}

func (c *Client) getEventsFromType(count int, entity string, ch chan []Event, wg *sync.WaitGroup) error {
	defer (*wg).Done()

	oneDayAgo := time.Now().Local().Add(- time.Hour).Unix()

	var ev []Event
	queryInput := &dynamodb.QueryInput{
		KeyConditions: map[string]*dynamodb.Condition{
			"host": {
				ComparisonOperator: aws.String("EQ"),
				AttributeValueList: []*dynamodb.AttributeValue{
					{
						S: aws.String(entity),
					},
				},
			},
			"timestamp": {
				ComparisonOperator: aws.String("GE"),
				AttributeValueList: []*dynamodb.AttributeValue{
					{
						N: aws.String(strconv.FormatInt(oneDayAgo, 10)),
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
		return err
	}

	err = dynamodbattribute.UnmarshalListOfMaps(result.Items, &ev)
	if err != nil {
		fmt.Println("Got error unmarshalling events")
		fmt.Println(err.Error())
		return err
	}
	ch <- ev
	return nil
}

func (c *Client) getEvents(count int) (events []Event, err error) {
	ch := make(chan []Event)
	var wg sync.WaitGroup

	wg.Add(1)
	go c.getEventsFromType(count, "yt", ch, &wg)

	wg.Add(1)
	go c.getEventsFromType(count, "dg", ch, &wg)

	go func() {
		wg.Wait()
		close(ch)
	}()

	for entityEvents := range ch {
		events = append(events, entityEvents...)
	}

	// Mix both YT and DG events
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp < events[j].Timestamp
	})

	return events, nil
}