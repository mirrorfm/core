package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/webhook"
)

type CreditPackage struct {
	Credits int
	Price   int64 // cents
}

var creditPackages = map[string]CreditPackage{
	"10": {Credits: 10, Price: 2000},
}

func (client *Client) handleGetCredits(c *gin.Context) {
	uid, _ := c.Get("firebase_uid")

	result, err := client.DynamoDB.GetItem(&dynamodb.GetItemInput{
		TableName: aws.String(client.DynamoDBUsersTable),
		Key: map[string]*dynamodb.AttributeValue{
			"user_id": {S: aws.String(uid.(string))},
		},
		ProjectionExpression: aws.String("credit_balance"),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get balance"})
		return
	}

	balance := 0
	if result.Item != nil {
		if bal, ok := result.Item["credit_balance"]; ok && bal.N != nil {
			balance, _ = strconv.Atoi(*bal.N)
		}
	}

	c.JSON(http.StatusOK, gin.H{"balance": balance})
}

func (client *Client) handleCheckout(c *gin.Context) {
	var req struct {
		PackageID string `json:"package_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	pkg, ok := creditPackages[req.PackageID]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid package"})
		return
	}

	uid, _ := c.Get("firebase_uid")
	email, _ := c.Get("email")

	var successURL, cancelURL string
	if isLocal {
		successURL = "http://localhost:5173/wallet/?success=true"
		cancelURL = "http://localhost:5173/wallet/?canceled=true"
	} else {
		successURL = "https://mirror.fm/wallet/?success=true"
		cancelURL = "https://mirror.fm/wallet/?canceled=true"
	}

	params := &stripe.CheckoutSessionParams{
		Mode: stripe.String(string(stripe.CheckoutSessionModePayment)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					Currency: stripe.String("usd"),
					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Name: stripe.String(strconv.Itoa(pkg.Credits) + " Mirror.FM Credits"),
					},
					UnitAmount: stripe.Int64(pkg.Price),
				},
				Quantity: stripe.Int64(1),
			},
		},
		SuccessURL: stripe.String(successURL),
		CancelURL:  stripe.String(cancelURL),
		ClientReferenceID: stripe.String(uid.(string)),
	}

	if emailStr, ok := email.(string); ok && emailStr != "" {
		params.CustomerEmail = stripe.String(emailStr)
	}

	params.AddMetadata("credits", strconv.Itoa(pkg.Credits))
	params.AddMetadata("user_id", uid.(string))

	s, err := session.New(params)
	if err != nil {
		log.Printf("Stripe checkout error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create checkout session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"url": s.URL})
}

func (client *Client) handleStripeWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	event, err := webhook.ConstructEvent(body, c.GetHeader("Stripe-Signature"), client.StripeWebhookSecret)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid signature"})
		return
	}

	if event.Type != "checkout.session.completed" {
		c.JSON(http.StatusOK, gin.H{"received": true})
		return
	}

	var sess stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to parse session"})
		return
	}

	userID := sess.ClientReferenceID
	creditsStr := sess.Metadata["credits"]
	credits, _ := strconv.Atoi(creditsStr)

	if userID == "" || credits <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing metadata"})
		return
	}

	// Atomically add credits to user balance
	_, err = client.DynamoDB.UpdateItem(&dynamodb.UpdateItemInput{
		TableName: aws.String(client.DynamoDBUsersTable),
		Key: map[string]*dynamodb.AttributeValue{
			"user_id": {S: aws.String(userID)},
		},
		UpdateExpression: aws.String("ADD credit_balance :c"),
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":c": {N: aws.String(strconv.Itoa(credits))},
		},
	})
	if err != nil {
		log.Printf("Failed to credit user %s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to credit balance"})
		return
	}

	// Record transaction in MySQL
	client.recordCreditTxn(userID, "purchase", credits, sess.ID)
	log.Printf("Credited %d credits to user %s (session %s)", credits, userID, sess.ID)
	c.JSON(http.StatusOK, gin.H{"received": true})
}
