package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	"firebase.google.com/go/v4/auth"
	"github.com/aws/aws-lambda-go/events"
	"github.com/stripe/stripe-go/v82"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
	"github.com/pkg/errors"
)

type Client struct {
	DynamoDB                     *dynamodb.DynamoDB
	DynamoDBEventsTable          string
	DynamoDBTracksTable          string
	DynamoDBDuplicateTracksTable string
	DynamoDBInterestsTable       string
	DynamoDBUsersTable           string
	DynamoDBTakedownsTable       string
	SQLDriver                    *sql.DB
	SpotifyClientID              string
	SpotifyClientSecret          string
	FirebaseAuth                 *auth.Client
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

	// Firebase Auth
	var firebaseAuth *auth.Client
	if firebaseProjectID := os.Getenv("FIREBASE_PROJECT_ID"); firebaseProjectID != "" {
		firebaseAuth = initFirebaseAuth(firebaseProjectID)
	} else {
		log.Println("FIREBASE_PROJECT_ID not set, auth endpoints disabled")
	}

	// Stripe
	if stripeKey := os.Getenv("STRIPE_SECRET_KEY"); stripeKey != "" {
		stripe.Key = stripeKey
	}

	client := Client{
		DynamoDB:                     dynamoClient,
		DynamoDBEventsTable:          "mirrorfm_events",
		DynamoDBTracksTable:          "mirrorfm_yt_tracks",
		DynamoDBDuplicateTracksTable: "mirrorfm_yt_duplicates",
		DynamoDBInterestsTable:       "mirrorfm_interests",
		DynamoDBUsersTable:           "mirrorfm_users",
		DynamoDBTakedownsTable:       "mirrorfm_takedowns",
		SQLDriver:                    sqlDriver,
		SpotifyClientID:              os.Getenv("SPOTIFY_CLIENT_ID"),
		SpotifyClientSecret:          os.Getenv("SPOTIFY_CLIENT_SECRET"),
		FirebaseAuth:                 firebaseAuth,
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
		params := parsePaginationParams(c)
		totalTracks, err := client.getTableCount(client.DynamoDBTracksTable)
		handleAPIError(c, err)
		foundTracks, err := client.getTableCount(client.DynamoDBDuplicateTracksTable)
		handleAPIError(c, err)
		channels, totalCount, err := client.getYoutubeChannelsPaginated(params, genreLimit)
		handleAPIError(c, err)
		allGenres, err := client.getDistinctGenres("yt_genres")
		handleAPIError(c, err)

		c.JSON(200, gin.H{
			"youtube":      channels,
			"total_count":  totalCount,
			"page":         params.Page,
			"per_page":     params.PerPage,
			"total_tracks": totalTracks,
			"found_tracks": foundTracks,
			"all_genres":   allGenres,
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
		params := parsePaginationParams(c)
		totalTracks, err := client.getTableCount(client.DynamoDBTracksTable)
		if err != nil {
			handleAPIError(c, errors.Wrap(err, "couldn't get table count"))
			return
		}
		foundTracks, err := client.getTableCount(client.DynamoDBDuplicateTracksTable)
		if err != nil {
			handleAPIError(c, errors.Wrap(err, "couldn't get found tracks"))
			return
		}
		labels, totalCount, err := client.getDiscogsLabelsPaginated(params, genreLimit)
		if err != nil {
			handleAPIError(c, errors.Wrap(err, "couldn't get discogs labels"))
			return
		}
		allGenres, err := client.getDistinctGenres("yt_genres")
		if err != nil {
			handleAPIError(c, errors.Wrap(err, "couldn't get genres"))
			return
		}

		c.JSON(200, gin.H{
			"discogs":      labels,
			"total_count":  totalCount,
			"page":         params.Page,
			"per_page":     params.PerPage,
			"total_tracks": totalTracks,
			"found_tracks": foundTracks,
			"all_genres":   allGenres,
		})
	})

	r.GET("/labels/:id", func(c *gin.Context) {
		label, err := client.getDiscogsLabel(c.Param("id"))
		if err != nil {
			handleAPIError(c, errors.Wrap(err, "couldn't get discogs label"))
			return
		}

		c.JSON(200, gin.H{
			"label": label,
		})
	})

	r.POST("/submit/analyze", func(c *gin.Context) {
		client.handleAnalyze(c)
	})

	r.POST("/submit/interest", func(c *gin.Context) {
		client.handleInterest(c)
	})

	// Takedown requests (public, no auth required)
	r.POST("/takedown", func(c *gin.Context) {
		client.handleTakedown(c)
	})

	// Auth routes (only if Firebase is configured)
	if client.FirebaseAuth != nil {
		authGroup := r.Group("/auth")
		authGroup.Use(client.authMiddleware())
		authGroup.GET("/me", func(c *gin.Context) {
			client.handleMe(c)
		})

		// Pitch: free beta submit + paid checkout + confirm
		r.POST("/pitch/submit", client.authMiddleware(), func(c *gin.Context) {
			client.handlePitchFree(c)
		})
		r.POST("/pitch/checkout", client.authMiddleware(), func(c *gin.Context) {
			client.handlePitchCheckout(c)
		})
		r.POST("/pitch/confirm", client.authMiddleware(), func(c *gin.Context) {
			client.handlePitchConfirm(c)
		})

		// Submissions (auth required)
		subsGroup := r.Group("/submissions")
		subsGroup.Use(client.authMiddleware())
		subsGroup.GET("", func(c *gin.Context) {
			client.handleListSubmissions(c)
		})
		subsGroup.PUT("/:id/respond", func(c *gin.Context) {
			client.handleRespondSubmission(c)
		})

		// Curator inbox (auth required)
		r.GET("/curator/submissions", client.authMiddleware(), func(c *gin.Context) {
			client.handleCuratorSubmissions(c)
		})
	}

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

	if c.Request.Method == "OPTIONS" {
		c.AbortWithStatus(204)
		return
	}
}

func handleAPIError(c *gin.Context, err error) {
	if err != nil {
		c.JSON(500, gin.H{
			"error": err.Error(),
		})
	}
}
