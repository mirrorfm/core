package main

import (
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type TakedownRequest struct {
	ID         string `json:"id" dynamodbav:"id"`
	ChannelURL string `json:"channel_url" dynamodbav:"channel_url"`
	Email      string `json:"email" dynamodbav:"email"`
	Message    string `json:"message" dynamodbav:"message"`
	CreatedAt  string `json:"created_at" dynamodbav:"created_at"`
	Status     string `json:"status" dynamodbav:"status"`
}

func (client *Client) handleTakedown(c *gin.Context) {
	var req struct {
		ChannelURL string `json:"channel_url"`
		Email      string `json:"email"`
		Message    string `json:"message"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	if req.ChannelURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "channel_url is required"})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	id := uuid.New().String()

	_, err := client.DynamoDB.PutItem(&dynamodb.PutItemInput{
		TableName: aws.String(client.DynamoDBTakedownsTable),
		Item: map[string]*dynamodb.AttributeValue{
			"id":          {S: aws.String(id)},
			"channel_url": {S: aws.String(req.ChannelURL)},
			"email":       {S: aws.String(req.Email)},
			"message":     {S: aws.String(req.Message)},
			"created_at":  {S: aws.String(now)},
			"status":      {S: aws.String("pending")},
		},
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save request"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id, "status": "pending"})
}

// POST /report — report an incorrect track in a playlist (public, no auth)
func (client *Client) handleReport(c *gin.Context) {
	var req struct {
		TrackURL    string `json:"track_url"`
		PlaylistURL string `json:"playlist_url"`
		Reason      string `json:"reason"`
	}

	if err := c.ShouldBindJSON(&req); err != nil || req.TrackURL == "" || req.PlaylistURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "track_url and playlist_url are required"})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	id := uuid.New().String()

	_, err := client.DynamoDB.PutItem(&dynamodb.PutItemInput{
		TableName: aws.String(client.DynamoDBTakedownsTable),
		Item: map[string]*dynamodb.AttributeValue{
			"id":           {S: aws.String(id)},
			"channel_url":  {S: aws.String(req.PlaylistURL)},
			"email":        {S: aws.String("")},
			"message":      {S: aws.String("[report] track: " + req.TrackURL + " | reason: " + req.Reason)},
			"created_at":   {S: aws.String(now)},
			"status":       {S: aws.String("pending")},
		},
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save report"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id})
}
