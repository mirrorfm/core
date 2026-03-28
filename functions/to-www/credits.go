package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/webhook"
)

const pitchPriceCents = 500 // $5.00

// POST /pitch/submit — free beta: create submissions directly (no payment)
func (client *Client) handlePitchFree(c *gin.Context) {
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

	if err := c.ShouldBindJSON(&req); err != nil || req.TrackURL == "" || len(req.Channels) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "track_url and channels required"})
		return
	}

	uid, _ := c.Get("firebase_uid")
	userID := uid.(string)

	paymentID := uuid.New().String()
	channelsJSON, _ := json.Marshal(req.Channels)
	now := time.Now().UTC()

	// Create payment record (free, immediately completed)
	_, err := client.SQLDriver.Exec(
		`INSERT INTO payments (payment_id, user_id, track_url, track_name, track_artist, track_image, channels_json, amount_cents, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 0, 'completed', ?)`,
		paymentID, userID, req.TrackURL, req.TrackName, req.TrackArtist, req.TrackImage, string(channelsJSON), now,
	)
	if err != nil {
		log.Printf("Failed to create free payment record: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create submission"})
		return
	}

	// Create submissions for each channel
	count := 0
	for _, ch := range req.Channels {
		_, err := client.SQLDriver.Exec(
			`INSERT INTO submissions (submission_id, payment_id, artist_user_id, channel_id, channel_name, track_url, track_name, track_artist, track_image, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)`,
			uuid.New().String(), paymentID, userID, ch.ID, ch.Name, req.TrackURL, req.TrackName, req.TrackArtist, req.TrackImage, now,
		)
		if err != nil {
			log.Printf("Failed to create submission for channel %s: %v", ch.ID, err)
			continue
		}
		count++
	}

	log.Printf("Free pitch: %d submissions created for user %s (payment %s)", count, userID, paymentID)
	c.JSON(http.StatusOK, gin.H{"submitted": count, "payment_id": paymentID})
}

// POST /pitch/checkout — create Stripe Checkout for a track submission
func (client *Client) handlePitchCheckout(c *gin.Context) {
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

	if err := c.ShouldBindJSON(&req); err != nil || req.TrackURL == "" || len(req.Channels) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "track_url and channels required"})
		return
	}

	uid, _ := c.Get("firebase_uid")
	email, _ := c.Get("email")
	userID := uid.(string)

	// Store pitch data in payments table (pending until Stripe confirms)
	paymentID := uuid.New().String()
	channelsJSON, _ := json.Marshal(req.Channels)

	_, err := client.SQLDriver.Exec(
		`INSERT INTO payments (payment_id, user_id, track_url, track_name, track_artist, track_image, channels_json, amount_cents, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)`,
		paymentID, userID, req.TrackURL, req.TrackName, req.TrackArtist, req.TrackImage, string(channelsJSON), pitchPriceCents, time.Now().UTC(),
	)
	if err != nil {
		log.Printf("Failed to create payment record: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create payment"})
		return
	}

	var successURL, cancelURL string
	if isLocal {
		successURL = "http://localhost:5173/pitch/?success=true"
		cancelURL = "http://localhost:5173/pitch/?canceled=true"
	} else {
		successURL = "https://mirror.fm/pitch/?success=true"
		cancelURL = "https://mirror.fm/pitch/?canceled=true"
	}

	params := &stripe.CheckoutSessionParams{
		Mode: stripe.String(string(stripe.CheckoutSessionModePayment)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					Currency: stripe.String("usd"),
					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Name: stripe.String("Track Submission — " + req.TrackName),
					},
					UnitAmount: stripe.Int64(pitchPriceCents),
				},
				Quantity: stripe.Int64(1),
			},
		},
		SuccessURL:        stripe.String(successURL),
		CancelURL:         stripe.String(cancelURL),
		ClientReferenceID: stripe.String(userID),
	}

	if emailStr, ok := email.(string); ok && emailStr != "" {
		params.CustomerEmail = stripe.String(emailStr)
	}

	params.AddMetadata("payment_id", paymentID)
	params.AddMetadata("user_id", userID)

	s, err := session.New(params)
	if err != nil {
		log.Printf("Stripe checkout error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create checkout session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"url": s.URL})
}

// POST /stripe/webhook — handle Stripe payment confirmation
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

	paymentID := sess.Metadata["payment_id"]
	if paymentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing payment_id"})
		return
	}

	// Look up payment record
	var userID, trackURL, trackName, trackArtist, trackImage, channelsJSONStr string
	err = client.SQLDriver.QueryRow(
		`SELECT user_id, track_url, track_name, track_artist, track_image, channels_json FROM payments WHERE payment_id = ? AND status = 'pending'`,
		paymentID,
	).Scan(&userID, &trackURL, &trackName, &trackArtist, &trackImage, &channelsJSONStr)
	if err != nil {
		log.Printf("Payment not found or already processed: %s - %v", paymentID, err)
		c.JSON(http.StatusOK, gin.H{"received": true})
		return
	}

	// Parse channels
	var channels []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(channelsJSONStr), &channels); err != nil {
		log.Printf("Failed to parse channels JSON for payment %s: %v", paymentID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse channels"})
		return
	}

	// Create submissions for each channel
	now := time.Now().UTC()
	for _, ch := range channels {
		_, err := client.SQLDriver.Exec(
			`INSERT INTO submissions (submission_id, payment_id, artist_user_id, channel_id, channel_name, track_url, track_name, track_artist, track_image, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)`,
			uuid.New().String(), paymentID, userID, ch.ID, ch.Name, trackURL, trackName, trackArtist, trackImage, now,
		)
		if err != nil {
			log.Printf("Failed to create submission for channel %s: %v", ch.ID, err)
		}
	}

	// Mark payment as completed
	_, _ = client.SQLDriver.Exec(
		`UPDATE payments SET status = 'completed', stripe_session_id = ? WHERE payment_id = ?`,
		sess.ID, paymentID,
	)

	log.Printf("Payment %s completed: %d submissions created for user %s", paymentID, len(channels), userID)
	c.JSON(http.StatusOK, gin.H{"received": true})
}
