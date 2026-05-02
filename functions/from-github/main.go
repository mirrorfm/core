package main

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
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
	"github.com/aws/aws-sdk-go/service/sqs"
	_ "github.com/go-sql-driver/mysql"
	"github.com/google/go-github/v33/github"
	"github.com/pkg/errors"
)

// The function has two roles, dispatched by event shape:
//
//  1. Webhook ingest (Lambda Function URL trigger): verifies the GitHub
//     X-Hub-Signature-256 HMAC, enqueues the raw body to SQS, returns 200.
//     Kept dead-simple so cold-start failure surface is minimal.
//
//  2. Consumer (SQS event source mapping): parses the enqueued PushEvent and
//     runs the existing CSV ingest logic (Dynamo cursor → MySQL insert →
//     SNS publish). Failures are retried by SQS up to maxReceiveCount, then
//     land in the DLQ — so transient DDB/MySQL/GitHub-raw blips no longer
//     drop CSV rows.

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

// --- Dispatcher ---

func dispatch(ctx context.Context, raw json.RawMessage) (any, error) {
	if isSQSEvent(raw) {
		var evt events.SQSEvent
		if err := json.Unmarshal(raw, &evt); err != nil {
			return nil, errors.Wrap(err, "decode SQSEvent")
		}
		return nil, consumerHandler(ctx, evt)
	}
	if isLambdaURLRequest(raw) {
		var req events.LambdaFunctionURLRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, errors.Wrap(err, "decode LambdaFunctionURLRequest")
		}
		return webhookHandler(ctx, req), nil
	}
	// Direct invocation (local runs, test events)
	var push github.PushEvent
	if err := json.Unmarshal(raw, &push); err != nil {
		return nil, errors.Wrap(err, "unrecognized event shape")
	}
	return nil, ProcessPushEvent(ctx, push)
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

func isLambdaURLRequest(raw json.RawMessage) bool {
	var probe struct {
		Version        string `json:"version"`
		RequestContext struct {
			HTTP struct {
				Method string `json:"method"`
			} `json:"http"`
		} `json:"requestContext"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Version == "2.0" && probe.RequestContext.HTTP.Method != ""
}

// --- Webhook handler (Lambda Function URL) ---

func webhookHandler(ctx context.Context, req events.LambdaFunctionURLRequest) events.LambdaFunctionURLResponse {
	body := req.Body
	if req.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			fmt.Printf("base64 decode failed: %s\n", err)
			return textResponse(400, "bad body")
		}
		body = string(decoded)
	}

	secret := os.Getenv("GITHUB_WEBHOOK_SECRET")
	if secret == "" || secret == "REPLACE_ME_AFTER_APPLY" {
		fmt.Println("GITHUB_WEBHOOK_SECRET not configured")
		return textResponse(500, "server not configured")
	}

	signature := headerLookup(req.Headers, "x-hub-signature-256")
	if !verifyHMAC([]byte(body), signature, secret) {
		fmt.Println("HMAC mismatch — rejecting request")
		return textResponse(401, "invalid signature")
	}

	queueURL := os.Getenv("WEBHOOK_QUEUE_URL")
	if queueURL == "" {
		fmt.Println("WEBHOOK_QUEUE_URL not set")
		return textResponse(500, "server not configured")
	}

	sess := session.Must(session.NewSession(&aws.Config{Region: aws.String(region)}))
	if _, err := sqs.New(sess).SendMessageWithContext(ctx, &sqs.SendMessageInput{
		QueueUrl:    aws.String(queueURL),
		MessageBody: aws.String(body),
	}); err != nil {
		fmt.Printf("SQS SendMessage failed: %s\n", err)
		return textResponse(500, "enqueue failed")
	}

	return textResponse(200, "ok")
}

func verifyHMAC(body []byte, signatureHeader, secret string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(signatureHeader, prefix) {
		return false
	}
	expected, err := hex.DecodeString(strings.TrimPrefix(signatureHeader, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(expected, mac.Sum(nil))
}

func headerLookup(h map[string]string, key string) string {
	// Lambda Function URL lowercases header names, but be defensive.
	if v, ok := h[key]; ok {
		return v
	}
	for k, v := range h {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func textResponse(status int, body string) events.LambdaFunctionURLResponse {
	return events.LambdaFunctionURLResponse{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "text/plain"},
		Body:       body,
	}
}

// --- Consumer handler (SQS) ---

func consumerHandler(ctx context.Context, evt events.SQSEvent) error {
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

// --- Existing CSV ingest logic (unchanged behavior, just renamed) ---

func ProcessPushEvent(ctx context.Context, evt github.PushEvent) error {
	fmt.Printf("%+v\n", evt)
	if evt.Repo == nil || evt.Repo.FullName == nil || evt.HeadCommit == nil || evt.HeadCommit.Modified == nil {
		fmt.Println("ignored incorrect event: some fields missing")
		return nil
	}

	app, err := getApp(ctx)
	if err != nil {
		return errors.Wrap(err, "could not set up app")
	}

	for _, file := range evt.HeadCommit.Modified {
		current, err := app.ProcessFile(*evt.Repo.FullName, file)
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
			fmt.Printf("skip duplicate #%d: %s\n", current, err.Error())
			continue
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
	// Local run: simulate a direct PushEvent invoke for both files.
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
