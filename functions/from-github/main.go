package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/sns"
	"github.com/go-sql-driver/mysql"
	"github.com/google/go-github/v33/github"
	"github.com/pkg/errors"
)

// SQS consumer for the from-github queue.
//
// The request path is API Gateway HTTP API → SQS direct integration, so this
// Lambda is only ever triggered by SQS records. Each record's body is the raw
// GitHub PushEvent JSON forwarded by API GW. Failures bubble up to the Lambda
// runtime; SQS retries up to maxReceiveCount (5) before parking the message
// in the DLQ. GitHub never sees a 5xx — API Gateway acks each delivery the
// moment SQS accepts the message.
//
// A direct-invocation fallback (PushEvent JSON) is kept for local runs and
// `aws lambda invoke` testing.

type App struct {
	DynamoDB     *dynamodb.DynamoDB
	CursorTable  string
	SQLDriver    *sql.DB
	SNSClient    *sns.SNS
	Region       string
	AwsAccountId string
}

type Category struct {
	GithubFile   string
	SQLTable     string
	SNSTopic     string
	DynamoCursor string
	IDField      string
	NameField    string
}

const region = "eu-west-1"

var categories = map[string]Category{
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
		"arn:aws:sns:%s:%s:mirrorfm_incoming_discogs_label",
		"from_github_last_successful_label",
		"label_id",
		"label_name",
	},
}

func dispatch(ctx context.Context, raw json.RawMessage) error {
	if isSQSEvent(raw) {
		var evt events.SQSEvent
		if err := json.Unmarshal(raw, &evt); err != nil {
			return errors.Wrap(err, "decode SQSEvent")
		}
		for _, record := range evt.Records {
			var push github.PushEvent
			if err := json.Unmarshal([]byte(record.Body), &push); err != nil {
				return errors.Wrap(err, "decode PushEvent from SQS body")
			}
			if err := ProcessPushEvent(ctx, push); err != nil {
				return err
			}
		}
		return nil
	}
	var push github.PushEvent
	if err := json.Unmarshal(raw, &push); err != nil {
		return errors.Wrap(err, "unrecognized event shape")
	}
	return ProcessPushEvent(ctx, push)
}

func isSQSEvent(raw json.RawMessage) bool {
	var probe struct {
		Records []struct {
			EventSource string `json:"eventSource"`
		} `json:"Records"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return len(probe.Records) > 0 && probe.Records[0].EventSource == "aws:sqs"
}

// dataRepo is hardcoded so the webhook body cannot redirect the fetch at a
// foreign repo. The webhook URL is publicly discoverable and HMAC is not
// verified at ingress; without this, an attacker could POST arbitrary
// repository.full_name and have us ingest channel IDs from any public repo.
const dataRepo = "mirrorfm/data"

func ProcessPushEvent(ctx context.Context, evt github.PushEvent) error {
	fmt.Printf("%+v\n", evt)
	if evt.HeadCommit == nil || evt.HeadCommit.Modified == nil {
		fmt.Println("ignored incorrect event: some fields missing")
		return nil
	}

	app, err := getApp(ctx)
	if err != nil {
		return errors.Wrap(err, "could not set up app")
	}

	for _, file := range evt.HeadCommit.Modified {
		if _, ok := categories[file]; !ok {
			continue // ignore changes to other files
		}
		current, err := app.ProcessFile(dataRepo, file)
		if err != nil {
			return errors.Wrap(err, fmt.Sprintf("could not process file %s", file))
		}
		if err := app.SaveCursor(categories[file].DynamoCursor, current); err != nil {
			return errors.Wrap(err, fmt.Sprintf("could not save cursor %d for file %s", current, categories[file].DynamoCursor))
		}
	}

	return nil
}

func getApp(ctx context.Context) (App, error) {
	dbHost := os.Getenv("DB_HOST")
	dbUser := os.Getenv("DB_USERNAME")
	dbPass := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	sqlDriver, err := sql.Open("mysql", dbUser+":"+dbPass+"@tcp("+dbHost+")/"+dbName+"?parseTime=true")
	if err != nil {
		return App{}, errors.Wrap(err, "failed to set up DB client")
	}

	sess := session.Must(session.NewSessionWithOptions(session.Options{
		SharedConfigState: session.SharedConfigEnable,
	}))
	dynamoClient := dynamodb.New(sess, &aws.Config{Region: aws.String(region)})
	snsClient := sns.New(sess, &aws.Config{Region: aws.String(region)})

	awsAccountId, ok := os.LookupEnv("AWS_ACCOUNT_ID")
	if !ok {
		lc, ok := lambdacontext.FromContext(ctx)
		if !ok {
			return App{}, errors.New("missing environment variable AWS_ACCOUNT_ID")
		}
		awsAccountId = strings.Split(lc.InvokedFunctionArn, ":")[4]
	}

	return App{
		DynamoDB:     dynamoClient,
		CursorTable:  "mirrorfm_cursors",
		SQLDriver:    sqlDriver,
		SNSClient:    snsClient,
		Region:       region,
		AwsAccountId: awsAccountId,
	}, nil
}

func (client *App) ProcessFile(repo, file string) (int, error) {
	url := strings.Join([]string{"https://raw.githubusercontent.com", repo, "master", file}, "/")

	resp, err := http.Get(url)
	if err != nil {
		return 0, errors.Wrap(err, fmt.Sprintf("failed to get %s", url))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d for %s", resp.StatusCode, url)
	}

	var lines []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) == 0 {
		return 0, errors.New("nothing in file")
	}

	cat := categories[file]
	cat.SNSTopic = fmt.Sprintf(cat.SNSTopic, client.Region, client.AwsAccountId)

	current, err := client.GetCursor(cat.DynamoCursor)
	if err != nil {
		return 0, errors.Wrap(err, "could not get cursor")
	}

	current, err = client.processLines(lines, current, cat)
	if err != nil {
		return 0, errors.Wrap(err, "failed to process lines")
	}

	return current, nil
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
			if isDuplicateKey(err) {
				fmt.Printf("skip duplicate #%d: %s\n", current, id)
				continue
			}
			// Surface non-duplicate errors so the Lambda fails and SQS retries.
			// Historically these were silently swallowed as "skip duplicate",
			// which advanced the cursor past genuine failures and dropped rows
			// (e.g. overstand87, _epler_).
			return current - 1, errors.Wrap(err, fmt.Sprintf("insert #%d (%s) failed", current, id))
		}

		_, err = client.SNSClient.Publish(&sns.PublishInput{
			TopicArn: aws.String(cat.SNSTopic),
			Message:  aws.String(id),
		})
		if err != nil {
			return current, errors.Wrap(err, fmt.Sprintf("failed to publish %s on %s\n", id, cat.SNSTopic))
		}
		fmt.Printf("published %s on %s\n", id, cat.SNSTopic)
	}

	return current, nil
}

// isDuplicateKey returns true if err is the MySQL "Duplicate entry" error
// (code 1062). Any other error must NOT be silently treated as a skip — that
// historically lost rows when the cursor advanced past genuine failures.
func isDuplicateKey(err error) bool {
	var mysqlErr *mysql.MySQLError
	cause := errors.Cause(err)
	if me, ok := cause.(*mysql.MySQLError); ok {
		mysqlErr = me
	}
	return mysqlErr != nil && mysqlErr.Number == 1062
}

func (client *App) InsertIntoTable(id, name string, cat Category) error {
	_, err := client.SQLDriver.Exec(fmt.Sprintf(`
		INSERT INTO %s (%s, %s, added_datetime)
		VALUES (?, ?, ?)
	`, cat.SQLTable, cat.IDField, cat.NameField), id, strings.TrimSpace(name), time.Now())
	if err != nil {
		return errors.Wrap(err, fmt.Sprintf("failed to insert into %s", cat.SQLTable))
	}
	return nil
}

func (client *App) GetCursor(cursor string) (int, error) {
	resp, err := client.DynamoDB.GetItem(&dynamodb.GetItemInput{
		TableName: &client.CursorTable,
		Key: map[string]*dynamodb.AttributeValue{
			"name": {S: aws.String(cursor)},
		},
		AttributesToGet: []*string{aws.String("value")},
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
			"name":  {S: aws.String(cursor)},
			"value": {N: aws.String(strconv.Itoa(value))},
		},
	}); err != nil {
		return errors.Wrap(err, fmt.Sprintf("failed to save %s cursor", cursor))
	}
	fmt.Printf("successfully set cursor to %d\n", value)
	return nil
}

func main() {
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		lambda.Start(dispatch)
		return
	}
	name := "mirrorfm/data"
	if err := ProcessPushEvent(context.TODO(), github.PushEvent{
		Repo: &github.PushEventRepository{FullName: &name},
		HeadCommit: &github.HeadCommit{
			Modified: []string{"youtube-channels.csv", "discogs-labels.csv"},
		},
	}); err != nil {
		fmt.Println(err.Error())
	}
}
