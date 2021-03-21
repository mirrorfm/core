package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/sns"
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/go-github/v33/github"
	"github.com/pkg/errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type App struct {
	DynamoDB    	*dynamodb.DynamoDB
	CursorTable		string
	SQLDriver       *sql.DB
	SNSClient       *sns.SNS
	Region          string
	AwsAccountId    string
}

type Category struct {
	GithubFile 		string // input
	SQLTable 		string // output
	SNSTopic        string // output
	DynamoCursor 	string
}

const (
	region = "eu-west-1"
)

var (
	categories = map[string]Category{
		"youtube-channels.csv": {
			"youtube-channels.csv",
			"yt_channels",
			"arn:aws:sns:%s:%s:mirrorfm_incoming_youtube_channel",
			"from_github_last_successful_channel",
		},
		"discogs-labels.csv": {
			"discogs-labels.csv",
			"dg_labels",
			"arn:aws:sns:%s:%s:mirrorfm_incoming_discogs_labels",
			"from_github_last_successful_label",
		},
	}
)

func getApp() (App, error) {
	// MySQL
	dbHost := os.Getenv("DB_HOST")
	dbUser := os.Getenv("DB_USERNAME")
	dbPass := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	sqlDriver, err := sql.Open("mysql", dbUser+":"+dbPass+"@tcp("+dbHost+")/"+dbName+"?parseTime=true")
	if err != nil {
		return App{}, errors.Wrap(err, "failed to set up DB client")
	}

	// AWS
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))
	dynamoClient := dynamodb.New(sess, &aws.Config{
		Region: aws.String(region),
	})
	snsClient := sns.New(sess, &aws.Config{
		Region: aws.String(region),
	})

	AwsAccountId, exists := os.LookupEnv("AWS_ACCOUNT_ID")
	if !exists {
		return App{}, errors.Errorf("missing environment variable AWS_ACCOUNT_ID")
	}

	return App{
		DynamoDB:   	dynamoClient,
		CursorTable: 	"mirrorfm_cursors",
		SQLDriver:  	sqlDriver,
		SNSClient:  	snsClient,
		Region: 		region,
		AwsAccountId:   AwsAccountId,
	}, nil
}

func Handler(evt github.PushEvent, ctx context.Context) error {
	app, err := getApp()
	if err != nil {
		return err
	}

	repo := *evt.Repo.FullName

	for _, file := range evt.HeadCommit.Modified {
		err := app.ProcessFile(repo, file)
		if err != nil {
			return err
		}
	}

	return nil
}

func (client *App) ProcessFile(repo, file string) error {
	s := []string{
		"https://raw.githubusercontent.com",
		repo,
		"master",
		file,
	}
	url := strings.Join(s, "/")

	resp, err := http.Get(url)
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("failed to get %s", url))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.New("Err")
	}

	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	fmt.Printf("%s", lines)

	cat := categories[file]

	current, err := client.GetCursor(cat.DynamoCursor)
	if err != nil {
		return err
	}

	current += 1
	total := len(lines) - 1

	if len(lines) == 0 {
		return errors.New("Nothing in file")
	}

	err = client.processLines(lines, current, total, cat)
	if err != nil {
		return err
	}

	return client.SaveCursor(cat.DynamoCursor, current)
}

func (client *App) processLines(lines []string, current, total int, cat Category) error {
	for current <= total {
		currentLine := lines[current]
		parts := strings.Split(currentLine, ",")
		id := parts[0]
		name := parts[1]

		if id == "" {
			fmt.Printf("Line %s is empty", id)
			break
		}

		err := client.InsertIntoTable(id, name, cat.SQLTable)
		if err != nil {
			fmt.Printf("Possibly a duplicate, nothing to do %s", err.Error())
		}

		_, err = client.SNSClient.Publish(&sns.PublishInput{
			TopicArn: aws.String(fmt.Sprintf(cat.SNSTopic, client.Region, client.AwsAccountId)),
			Message:  aws.String(id),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (client *App) InsertIntoTable(id, name, tableName string) error {
	_, err := client.SQLDriver.Exec(fmt.Sprintf(`
		INSERT INTO %s (id, name, added_datetime)
		VALUES (?, ?, ?)
	`, tableName), id, name, time.Now())
	return errors.Wrap(err, fmt.Sprintf("failed to insert into %s", tableName))
}

func (client *App) GetCursor(cursor string) (int, error) {
	resp, err := client.DynamoDB.GetItem(&dynamodb.GetItemInput{
		TableName: &client.CursorTable,
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

	val, ok := resp.Item["name"]
	if ok {
		return 0, nil
	}

	return strconv.Atoi(*val.S)
}

func (client *App) SaveCursor(cursor string, value int) error {
	_, err := client.DynamoDB.PutItem(&dynamodb.PutItemInput{
		TableName: &client.CursorTable,
		Item: map[string]*dynamodb.AttributeValue{
			"name": {
				S: aws.String(cursor),
			},
			"value": {
				N: aws.String(strconv.Itoa(value - 1)),
			},
		},
	})

	return err
}

func main() {
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		lambda.Start(Handler)
	} else {
		// Local run
		name := "mirrorfm/data"
		err := Handler(github.PushEvent{
			Repo: &github.PushEventRepository{
				FullName: &name,
			},
			HeadCommit: &github.HeadCommit{
				Modified: []string{
					"youtube-channels.csv",
					"discogs-labels.csv",
				},
			},
		}, context.TODO())
		if err != nil {
			fmt.Println(err.Error())
		}
	}
}