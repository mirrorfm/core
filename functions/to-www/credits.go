package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/checkout/session"
)

const pitchPriceCents = 500 // $5.00

// POST /pitch/submit — free beta: create submissions directly (no payment)
func (client *Client) handlePitchFree(c *gin.Context) {
	var req pitchRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.TrackURL == "" || len(req.Channels) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "track_url and channels required"})
		return
	}

	uid, _ := c.Get("firebase_uid")
	userID := uid.(string)

	paymentID := uuid.New().String()
	channelsJSON, _ := json.Marshal(req.Channels)
	now := time.Now().UTC()

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

	count := client.createSubmissionsForPayment(paymentID, userID, req.TrackURL, req.TrackName, req.TrackArtist, req.TrackImage, req.Channels, now)
	log.Printf("Free pitch: %d submissions created for user %s (payment %s)", count, userID, paymentID)
	c.JSON(http.StatusOK, gin.H{"submitted": count, "payment_id": paymentID})
}

// POST /pitch/checkout — create Stripe Checkout session
func (client *Client) handlePitchCheckout(c *gin.Context) {
	var req pitchRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.TrackURL == "" || len(req.Channels) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "track_url and channels required"})
		return
	}

	uid, _ := c.Get("firebase_uid")
	email, _ := c.Get("email")
	userID := uid.(string)

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
		successURL = "http://localhost:5173/pitch/?session_id={CHECKOUT_SESSION_ID}"
		cancelURL = "http://localhost:5173/pitch/?canceled=true"
	} else {
		successURL = "https://mirror.fm/pitch/?session_id={CHECKOUT_SESSION_ID}"
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

// POST /pitch/confirm — verify Stripe payment and create submissions
func (client *Client) handlePitchConfirm(c *gin.Context) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.SessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id required"})
		return
	}

	uid, _ := c.Get("firebase_uid")
	userID := uid.(string)

	// Retrieve session from Stripe to verify payment
	sess, err := session.Get(req.SessionID, nil)
	if err != nil {
		log.Printf("Stripe session retrieval error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session"})
		return
	}

	if sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		c.JSON(http.StatusPaymentRequired, gin.H{"error": "payment not completed"})
		return
	}

	// Verify the session belongs to this user
	if sess.ClientReferenceID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "session does not belong to this user"})
		return
	}

	paymentID := sess.Metadata["payment_id"]
	if paymentID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing payment_id in session"})
		return
	}

	// Look up payment record
	var trackURL, trackName, trackArtist, trackImage, channelsJSONStr, status string
	err = client.SQLDriver.QueryRow(
		`SELECT track_url, track_name, track_artist, track_image, channels_json, status FROM payments WHERE payment_id = ? AND user_id = ?`,
		paymentID, userID,
	).Scan(&trackURL, &trackName, &trackArtist, &trackImage, &channelsJSONStr, &status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "payment not found"})
		return
	}

	if status == "completed" {
		// Already confirmed (idempotent)
		c.JSON(http.StatusOK, gin.H{"status": "already_confirmed"})
		return
	}

	var channels []pitchChannel
	if err := json.Unmarshal([]byte(channelsJSONStr), &channels); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse channels"})
		return
	}

	now := time.Now().UTC()
	count := client.createSubmissionsForPayment(paymentID, userID, trackURL, trackName, trackArtist, trackImage, channels, now)

	_, _ = client.SQLDriver.Exec(
		`UPDATE payments SET status = 'completed', stripe_session_id = ? WHERE payment_id = ?`,
		sess.ID, paymentID,
	)

	log.Printf("Payment %s confirmed: %d submissions created for user %s", paymentID, count, userID)
	c.JSON(http.StatusOK, gin.H{"submitted": count, "payment_id": paymentID})
}

// shared types and helpers

type pitchChannel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type pitchRequest struct {
	TrackURL    string         `json:"track_url"`
	TrackName   string         `json:"track_name"`
	TrackArtist string         `json:"track_artist"`
	TrackImage  string         `json:"track_image"`
	Channels    []pitchChannel `json:"channels"`
}

func (client *Client) createSubmissionsForPayment(paymentID, userID, trackURL, trackName, trackArtist, trackImage string, channels []pitchChannel, now time.Time) int {
	count := 0
	for _, ch := range channels {
		_, err := client.SQLDriver.Exec(
			`INSERT INTO submissions (submission_id, payment_id, artist_user_id, channel_id, channel_name, track_url, track_name, track_artist, track_image, status, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?)`,
			uuid.New().String(), paymentID, userID, ch.ID, ch.Name, trackURL, trackName, trackArtist, trackImage, now,
		)
		if err != nil {
			log.Printf("Failed to create submission for channel %s: %v", ch.ID, err)
			continue
		}
		count++
	}
	return count
}
