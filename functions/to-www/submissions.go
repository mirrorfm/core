package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/gin-gonic/gin"
)

type Submission struct {
	SubmissionID string  `json:"submission_id"`
	PaymentID    string  `json:"payment_id"`
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

// GET /submissions — list artist's own submissions
func (client *Client) handleListSubmissions(c *gin.Context) {
	uid, _ := c.Get("firebase_uid")

	rows, err := client.SQLDriver.Query(
		`SELECT submission_id, payment_id, artist_user_id, channel_id, channel_name, track_url, track_name, track_artist, track_image, status, created_at, responded_at
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

	placeholders := make([]string, len(channelIDs))
	args := make([]interface{}, len(channelIDs))
	for i, id := range channelIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	args = append(args, "pending")

	rows, err := client.SQLDriver.Query(
		`SELECT submission_id, payment_id, artist_user_id, channel_id, channel_name, track_url, track_name, track_artist, track_image, status, created_at, responded_at
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

func scanSubmissions(rows interface{ Next() bool; Scan(...interface{}) error }) []Submission {
	var submissions []Submission
	for rows.Next() {
		var s Submission
		var respondedAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&s.SubmissionID, &s.PaymentID, &s.ArtistUserID, &s.ChannelID, &s.ChannelName,
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
