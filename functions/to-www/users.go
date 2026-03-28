package main

import (
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/gin-gonic/gin"
)

type User struct {
	UserID      string `json:"user_id" dynamodbav:"user_id"`
	Email       string `json:"email" dynamodbav:"email"`
	DisplayName string `json:"display_name" dynamodbav:"display_name"`
	PhotoURL    string `json:"photo_url" dynamodbav:"photo_url"`
	Provider    string `json:"provider" dynamodbav:"provider"`
	CreatedAt   string `json:"created_at" dynamodbav:"created_at"`
	LastLogin   string `json:"last_login" dynamodbav:"last_login"`
}

func (client *Client) getOrCreateUser(uid, email, displayName, photoURL string) (*User, error) {
	result, err := client.DynamoDB.GetItem(&dynamodb.GetItemInput{
		TableName: aws.String(client.DynamoDBUsersTable),
		Key: map[string]*dynamodb.AttributeValue{
			"user_id": {S: aws.String(uid)},
		},
	})
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)

	if result.Item != nil {
		var user User
		if err := dynamodbattribute.UnmarshalMap(result.Item, &user); err != nil {
			return nil, err
		}

		_, err = client.DynamoDB.UpdateItem(&dynamodb.UpdateItemInput{
			TableName: aws.String(client.DynamoDBUsersTable),
			Key: map[string]*dynamodb.AttributeValue{
				"user_id": {S: aws.String(uid)},
			},
			UpdateExpression: aws.String("SET last_login = :ll, email = :e, display_name = :dn, photo_url = :pu"),
			ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
				":ll": {S: aws.String(now)},
				":e":  {S: aws.String(email)},
				":dn": {S: aws.String(displayName)},
				":pu": {S: aws.String(photoURL)},
			},
		})
		if err != nil {
			return nil, err
		}

		user.LastLogin = now
		user.Email = email
		user.DisplayName = displayName
		user.PhotoURL = photoURL
		return &user, nil
	}

	user := User{
		UserID:      uid,
		Email:       email,
		DisplayName: displayName,
		PhotoURL:    photoURL,
		Provider:    "google",
		CreatedAt:   now,
		LastLogin:   now,
	}

	item, err := dynamodbattribute.MarshalMap(user)
	if err != nil {
		return nil, err
	}

	_, err = client.DynamoDB.PutItem(&dynamodb.PutItemInput{
		TableName: aws.String(client.DynamoDBUsersTable),
		Item:      item,
	})
	if err != nil {
		return nil, err
	}

	return &user, nil
}

func (client *Client) handleMe(c *gin.Context) {
	uid, _ := c.Get("firebase_uid")
	email, _ := c.Get("email")
	displayName, _ := c.Get("display_name")
	photoURL, _ := c.Get("photo_url")

	user, err := client.getOrCreateUser(
		uid.(string),
		valOrEmpty(email),
		valOrEmpty(displayName),
		valOrEmpty(photoURL),
	)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to get user"})
		return
	}

	c.JSON(200, gin.H{"user": user})
}

func valOrEmpty(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
