package main

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Submission struct {
	SubmissionID string  `json:"submission_id"`
	ArtistUserID string  `json:"artist_user_id"`
	ChannelID    string  `json:"channel_id"`
	ChannelName  string  `json:"channel_name"`
	TrackURL     string  `json:"track_url"`
	TrackName    string  `json:"track_name"`
	TrackArtist  string  `json:"track_artist"`
	TrackImage   string  `json:"track_image"`
	Status       string  `json:"status"`
	CreatedAt    string  `json:"created_at"`
	RespondedAt  *string `json:"responded_at,omitempty"`
}

// POST /submissions — artist submits track to selected channels, reserves credits
func (client *Client) handleCreateSubmissions(c *gin.Context) {
	var req struct {
		TrackURL    string `json:"track_url"`
		TrackName   string `json:"track_name"`
		TrackArtist string `json:"track_artist"`
		TrackImage  string `json:"track_image"`
		Channels    []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"channels"`
	}

	if err := c.ShouldBindJSON(&req); err != nil || len(req.Channels) == 0 || req.TrackURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "track_url and at least one channel required"})
		return
	}

	uid, _ := c.Get("firebase_uid")
	userID := uid.(string)
	creditsNeeded := len(req.Channels)

	// Reserve credits atomically (conditional decrement on DynamoDB user record)
	_, err := client.DynamoDB.UpdateItem(&dynamodb.UpdateItemInput{
		TableName: aws.String(client.DynamoDBUsersTable),
		Key: map[string]*dynamodb.AttributeValue{
			"user_id": {S: aws.String(userID)},
		},
		UpdateExpression:    aws.String("SET credit_balance = credit_balance - :cost"),
		ConditionExpression: aws.String("credit_balance >= :cost"),
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":cost": {N: aws.String(strconv.Itoa(creditsNeeded))},
		},
	})
	if err != nil {
		c.JSON(http.StatusPaymentRequired, gin.H{"error": "insufficient credits"})
		return
	}

	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	submissions := make([]Submission, 0, len(req.Channels))

	for _, ch := range req.Channels {
		id := uuid.New().String()
		_, err := client.SQLDriver.Exec(
			`INSERT INTO submissions (submission_id, artist_user_id, channel_id, channel_name, track_url, track_name, track_artist, track_image, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)`,
			id, userID, ch.ID, ch.Name, req.TrackURL, req.TrackName, req.TrackArtist, req.TrackImage, now,
		)
		if err != nil {
			log.Printf("Failed to insert submission: %v", err)
			continue
		}
		submissions = append(submissions, Submission{
			SubmissionID: id, ArtistUserID: userID, ChannelID: ch.ID, ChannelName: ch.Name,
			TrackURL: req.TrackURL, TrackName: req.TrackName, TrackArtist: req.TrackArtist,
			TrackImage: req.TrackImage, Status: "pending", CreatedAt: nowStr,
		})
	}

	// Record credit transaction
	client.recordCreditTxn(userID, "submission", -creditsNeeded, "")

	c.JSON(http.StatusOK, gin.H{"submissions": submissions})
}

// GET /submissions — list artist's own submissions
func (client *Client) handleListSubmissions(c *gin.Context) {
	uid, _ := c.Get("firebase_uid")

	rows, err := client.SQLDriver.Query(
		`SELECT submission_id, artist_user_id, channel_id, channel_name, track_url, track_name, track_artist, track_image, status, created_at, responded_at
		 FROM submissions WHERE artist_user_id = ? ORDER BY created_at DESC`, uid.(string),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch submissions"})
		return
	}
	defer rows.Close()

	submissions := scanSubmissions(rows)
	c.JSON(http.StatusOK, gin.H{"submissions": submissions})
}

// GET /curator/submissions — list pending submissions for curator's managed channels
func (client *Client) handleCuratorSubmissions(c *gin.Context) {
	uid, _ := c.Get("firebase_uid")

	// Get curator's managed channels from DynamoDB user record
	userResult, err := client.DynamoDB.GetItem(&dynamodb.GetItemInput{
		TableName: aws.String(client.DynamoDBUsersTable),
		Key: map[string]*dynamodb.AttributeValue{
			"user_id": {S: aws.String(uid.(string))},
		},
		ProjectionExpression: aws.String("managed_channels"),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get user"})
		return
	}

	var channelIDs []string
	if userResult.Item != nil {
		if mc, ok := userResult.Item["managed_channels"]; ok && mc.SS != nil {
			for _, s := range mc.SS {
				channelIDs = append(channelIDs, *s)
			}
		}
	}

	if len(channelIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"submissions": []Submission{}})
		return
	}

	// Build IN clause
	placeholders := make([]string, len(channelIDs))
	args := make([]interface{}, len(channelIDs))
	for i, id := range channelIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	args = append(args, "pending")

	rows, err := client.SQLDriver.Query(
		`SELECT submission_id, artist_user_id, channel_id, channel_name, track_url, track_name, track_artist, track_image, status, created_at, responded_at
		 FROM submissions WHERE channel_id IN (`+strings.Join(placeholders, ",")+`) AND status = ? ORDER BY created_at DESC`, args...,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch submissions"})
		return
	}
	defer rows.Close()

	submissions := scanSubmissions(rows)
	c.JSON(http.StatusOK, gin.H{"submissions": submissions})
}

// PUT /submissions/:id/respond — curator accepts or rejects
func (client *Client) handleRespondSubmission(c *gin.Context) {
	subID := c.Param("id")
	uid, _ := c.Get("firebase_uid")

	var req struct {
		Action string `json:"action"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || (req.Action != "accept" && req.Action != "reject") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "action must be 'accept' or 'reject'"})
		return
	}

	// Get submission's channel_id
	var channelID, status string
	err := client.SQLDriver.QueryRow(
		`SELECT channel_id, status FROM submissions WHERE submission_id = ?`, subID,
	).Scan(&channelID, &status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "submission not found"})
		return
	}
	if status != "pending" {
		c.JSON(http.StatusConflict, gin.H{"error": "submission already responded to"})
		return
	}

	// Verify curator manages this channel
	userResult, err := client.DynamoDB.GetItem(&dynamodb.GetItemInput{
		TableName: aws.String(client.DynamoDBUsersTable),
		Key: map[string]*dynamodb.AttributeValue{
			"user_id": {S: aws.String(uid.(string))},
		},
		ProjectionExpression: aws.String("managed_channels"),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify permissions"})
		return
	}

	authorized := false
	if userResult.Item != nil {
		if mc, ok := userResult.Item["managed_channels"]; ok && mc.SS != nil {
			for _, s := range mc.SS {
				if *s == channelID {
					authorized = true
					break
				}
			}
		}
	}
	if !authorized {
		c.JSON(http.StatusForbidden, gin.H{"error": "you don't manage this channel"})
		return
	}

	newStatus := "accepted"
	if req.Action == "reject" {
		newStatus = "rejected"
	}

	result, err := client.SQLDriver.Exec(
		`UPDATE submissions SET status = ?, responded_at = ? WHERE submission_id = ? AND status = 'pending'`,
		newStatus, time.Now().UTC(), subID,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update submission"})
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "submission already responded to"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"submission_id": subID, "status": newStatus})
}

// POST /submissions/expire — expire pending submissions older than 7 days, refund credits
func (client *Client) handleExpireSubmissions(c *gin.Context) {
	threshold := time.Now().UTC().Add(-7 * 24 * time.Hour)

	// Find pending submissions older than 7 days
	rows, err := client.SQLDriver.Query(
		`SELECT submission_id, artist_user_id FROM submissions WHERE status = 'pending' AND created_at < ?`, threshold,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query expired submissions"})
		return
	}
	defer rows.Close()

	type expiredSub struct {
		ID     string
		UserID string
	}
	var expired []expiredSub
	for rows.Next() {
		var s expiredSub
		if err := rows.Scan(&s.ID, &s.UserID); err != nil {
			continue
		}
		expired = append(expired, s)
	}

	now := time.Now().UTC()
	refunds := make(map[string]int)

	for _, sub := range expired {
		result, err := client.SQLDriver.Exec(
			`UPDATE submissions SET status = 'expired', responded_at = ? WHERE submission_id = ? AND status = 'pending'`,
			now, sub.ID,
		)
		if err != nil {
			log.Printf("Failed to expire submission %s: %v", sub.ID, err)
			continue
		}
		if affected, _ := result.RowsAffected(); affected > 0 {
			refunds[sub.UserID]++
		}
	}

	// Refund credits per user (DynamoDB atomic ADD)
	for userID, credits := range refunds {
		_, err := client.DynamoDB.UpdateItem(&dynamodb.UpdateItemInput{
			TableName: aws.String(client.DynamoDBUsersTable),
			Key: map[string]*dynamodb.AttributeValue{
				"user_id": {S: aws.String(userID)},
			},
			UpdateExpression: aws.String("ADD credit_balance :refund"),
			ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
				":refund": {N: aws.String(strconv.Itoa(credits))},
			},
		})
		if err != nil {
			log.Printf("Failed to refund %d credits to user %s: %v", credits, userID, err)
			continue
		}
		client.recordCreditTxn(userID, "refund_expired", credits, "")
		log.Printf("Refunded %d credits to user %s (expired submissions)", credits, userID)
	}

	c.JSON(http.StatusOK, gin.H{"expired_count": len(expired), "users_refunded": len(refunds)})
}

// recordCreditTxn logs a credit transaction to MySQL
func (client *Client) recordCreditTxn(userID, txnType string, credits int, stripeSessionID string) {
	_, err := client.SQLDriver.Exec(
		`INSERT INTO credit_txns (txn_id, user_id, type, credits, stripe_session_id, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.New().String(), userID, txnType, credits, stripeSessionID, time.Now().UTC(),
	)
	if err != nil {
		log.Printf("Failed to record credit txn for user %s: %v", userID, err)
	}
}

func scanSubmissions(rows interface{ Next() bool; Scan(...interface{}) error }) []Submission {
	var submissions []Submission
	for rows.Next() {
		var s Submission
		var respondedAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&s.SubmissionID, &s.ArtistUserID, &s.ChannelID, &s.ChannelName,
			&s.TrackURL, &s.TrackName, &s.TrackArtist, &s.TrackImage, &s.Status, &createdAt, &respondedAt); err != nil {
			continue
		}
		s.CreatedAt = createdAt.Format(time.RFC3339)
		if respondedAt != nil {
			t := respondedAt.Format(time.RFC3339)
			s.RespondedAt = &t
		}
		submissions = append(submissions, s)
	}
	if submissions == nil {
		submissions = []Submission{}
	}
	return submissions
}
