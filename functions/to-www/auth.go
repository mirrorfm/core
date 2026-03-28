package main

import (
	"context"
	"log"
	"net/http"
	"strings"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"github.com/gin-gonic/gin"
	"google.golang.org/api/option"
)

func initFirebaseAuth(projectID string) *auth.Client {
	ctx := context.Background()
	conf := &firebase.Config{ProjectID: projectID}
	app, err := firebase.NewApp(ctx, conf, option.WithoutAuthentication())
	if err != nil {
		log.Fatalf("Error initializing Firebase: %v", err)
	}
	authClient, err := app.Auth(ctx)
	if err != nil {
		log.Fatalf("Error getting Firebase auth client: %v", err)
	}
	return authClient
}

func (client *Client) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid authorization header"})
			c.Abort()
			return
		}

		idToken := strings.TrimPrefix(authHeader, "Bearer ")
		token, err := client.FirebaseAuth.VerifyIDToken(c.Request.Context(), idToken)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			c.Abort()
			return
		}

		c.Set("firebase_uid", token.UID)
		if email, ok := token.Claims["email"].(string); ok {
			c.Set("email", email)
		}
		if name, ok := token.Claims["name"].(string); ok {
			c.Set("display_name", name)
		}
		if picture, ok := token.Claims["picture"].(string); ok {
			c.Set("photo_url", picture)
		}

		c.Next()
	}
}
