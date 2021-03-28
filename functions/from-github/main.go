package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
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
	IDField  		string
	NameField       string
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
			"channel_id",
			"channel_name",
		},
		"discogs-labels.csv": {
			"discogs-labels.csv",
			"dg_labels",
			"arn:aws:sns:%s:%s:mirrorfm_incoming_discogs_labels",
			"from_github_last_successful_label",
			"label_id",
			"label_name",
		},
	}
)

func getApp(ctx context.Context) (App, error) {
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
		lc, ok := lambdacontext.FromContext(ctx)
		if !ok {
			return App{}, errors.Errorf("missing environment variable AWS_ACCOUNT_ID")
		}
		AwsAccountId = strings.Split(lc.InvokedFunctionArn, ":")[4]
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

func Handler(ctx context.Context, evt github.PushEvent) error {
	app, err := getApp(ctx)
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
		return errors.New(fmt.Sprintf("status %d for %s", resp.StatusCode, url))
	}

	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) == 0 {
		return errors.New("nothing in file")
	}

	cat := categories[file]

	current, err := client.GetCursor(cat.DynamoCursor)
	if err != nil {
		return err
	}

	current, err = client.processLines(lines, current, cat)
	if err != nil {
		return errors.Wrap(err, "failed to process lines")
	}

	return client.SaveCursor(cat.DynamoCursor, current)
}

func (client *App) processLines(lines []string, current int, cat Category) (int, error) {
	total := len(lines) - 1

	for current < total {
		current += 1
		currentLine := lines[current]

		parts := strings.Split(currentLine, ",")
		id := parts[0]
		name := parts[1]

		if id == "" {
			fmt.Printf("line %s is empty", id)
			break
		}

		err := client.InsertIntoTable(id, name, cat)
		if err != nil {
			fmt.Printf("skip duplicate #%d: %s\n", current, err.Error())
			continue
		}

		_, err = client.SNSClient.Publish(&sns.PublishInput{
			TopicArn: aws.String(fmt.Sprintf(cat.SNSTopic, client.Region, client.AwsAccountId)),
			Message:  aws.String(id),
		})
		if err != nil {
			return current, errors.Wrap(err, fmt.Sprintf("failed to publish %s on %s\n", id, cat.SNSTopic))
		}
		fmt.Printf("published %s on %s\n", id, cat.SNSTopic)
	}

	return current, nil
}

func (client *App) InsertIntoTable(id, name string, cat Category) error {
	_, err := client.SQLDriver.Exec(fmt.Sprintf(`
		INSERT INTO %s (%s, %s, added_datetime)
		VALUES (?, ?, ?)
	`, cat.SQLTable, cat.IDField, cat.NameField), id, name, time.Now())
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("failed to insert into %s", cat.SQLTable))
	}
	return nil
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

	val, ok := resp.Item["value"]
	if !ok {
		return 0, nil
	}

	return strconv.Atoi(*val.N)
}

func (client *App) SaveCursor(cursor string, value int) error {
	if _, err := client.DynamoDB.PutItem(&dynamodb.PutItemInput{
		TableName: &client.CursorTable,
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

func main() {
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		lambda.Start(Handler)
	} else {
		// Local run
		name := "mirrorfm/data"
		err := Handler(context.TODO(),
			github.PushEvent{
			Repo: &github.PushEventRepository{
				FullName: &name,
			},
			HeadCommit: &github.HeadCommit{
				Modified: []string{
					"youtube-channels.csv",
					"discogs-labels.csv",
				},
			},
		})
		if err != nil {
			fmt.Println(err.Error())
		}
	}
}