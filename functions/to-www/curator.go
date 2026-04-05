package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/gin-gonic/gin"
)

type youtubeChannelResponse struct {
	Items []struct {
		ID      string `json:"id"`
		Snippet struct {
			Title      string `json:"title"`
			CustomURL  string `json:"customUrl"`
			Thumbnails struct {
				Default struct {
					URL string `json:"url"`
				} `json:"default"`
			} `json:"thumbnails"`
		} `json:"snippet"`
	} `json:"items"`
}

type CuratorChannel struct {
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Thumbnail   string `json:"thumbnail"`
	Tracked     bool   `json:"tracked"` // true if we track this channel in yt_channels
}

// POST /curator/claim — verify YouTube channel ownership and link to user
func (client *Client) handleCuratorClaim(c *gin.Context) {
	var req struct {
		YouTubeAccessToken string `json:"youtube_access_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.YouTubeAccessToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "youtube_access_token required"})
		return
	}

	uid, _ := c.Get("firebase_uid")
	userID := uid.(string)

	// Call YouTube API to get user's channels
	ytReq, _ := http.NewRequest("GET", "https://www.googleapis.com/youtube/v3/channels?part=snippet&mine=true", nil)
	ytReq.Header.Set("Authorization", "Bearer "+req.YouTubeAccessToken)

	resp, err := http.DefaultClient.Do(ytReq)
	if err != nil {
		log.Printf("YouTube API error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify YouTube account"})
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		log.Printf("YouTube API returned %d: %s", resp.StatusCode, string(body))
		if resp.StatusCode == 403 && strings.Contains(string(body), "quota") {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "YouTube API quota exceeded. Please try again tomorrow."})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to fetch YouTube channels"})
		}
		return
	}

	var ytResp youtubeChannelResponse
	if err := json.Unmarshal(body, &ytResp); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse YouTube response"})
		return
	}

	if len(ytResp.Items) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "no YouTube channels found for this account"})
		return
	}

	// Check which channels we track
	var claimed []CuratorChannel
	var managedIDs []*dynamodb.AttributeValue

	for _, item := range ytResp.Items {
		ch := CuratorChannel{
			ChannelID:   item.ID,
			ChannelName: item.Snippet.Title,
			Thumbnail:   item.Snippet.Thumbnails.Default.URL,
		}

		// Check if this channel exists in our yt_channels table
		var count int
		err := client.SQLDriver.QueryRow("SELECT COUNT(*) FROM yt_channels WHERE channel_id = ?", item.ID).Scan(&count)
		if err == nil && count > 0 {
			ch.Tracked = true
			managedIDs = append(managedIDs, &dynamodb.AttributeValue{S: aws.String(item.ID)})
		}

		claimed = append(claimed, ch)
	}

	if len(managedIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"channels": claimed,
			"linked":   0,
			"message":  "Your channels are not currently tracked by Mirror.FM. We'll add them soon.",
		})
		return
	}

	// Prevent duplicate claims: check if any of these channels are already claimed
	for _, mid := range managedIDs {
		var claimedBy string
		err := client.SQLDriver.QueryRow(
			"SELECT user_id FROM channel_claims WHERE channel_id = ?", *mid.S,
		).Scan(&claimedBy)
		if err == nil && claimedBy != userID {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("Channel %s is already claimed by another user", *mid.S)})
			return
		}
	}

	// Record claims in MySQL (prevents future duplicates)
	for _, mid := range managedIDs {
		_, _ = client.SQLDriver.Exec(
			"INSERT IGNORE INTO channel_claims (channel_id, user_id, claimed_at) VALUES (?, ?, ?)",
			*mid.S, userID, time.Now().UTC(),
		)
	}

	// Update managed_channels on user record (DynamoDB String Set ADD)
	_, err = client.DynamoDB.UpdateItem(&dynamodb.UpdateItemInput{
		TableName: aws.String(client.DynamoDBUsersTable),
		Key: map[string]*dynamodb.AttributeValue{
			"user_id": {S: aws.String(userID)},
		},
		UpdateExpression: aws.String("ADD managed_channels :channels"),
		ExpressionAttributeValues: map[string]*dynamodb.AttributeValue{
			":channels": {SS: func() []*string {
				ids := make([]*string, len(managedIDs))
				for i, v := range managedIDs {
					ids[i] = v.S
				}
				return ids
			}()},
		},
	})
	if err != nil {
		log.Printf("Failed to update managed_channels for user %s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to link channels"})
		return
	}

	log.Printf("Curator %s claimed %d channels", userID, len(managedIDs))
	c.JSON(http.StatusOK, gin.H{
		"channels": claimed,
		"linked":   len(managedIDs),
	})
}

// GET /curator/channels — return curator's managed channels with details
func (client *Client) handleCuratorChannels(c *gin.Context) {
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
		c.JSON(http.StatusOK, gin.H{"channels": []CuratorChannel{}})
		return
	}

	// Fetch channel details from MySQL
	placeholders := make([]string, len(channelIDs))
	args := make([]interface{}, len(channelIDs))
	for i, id := range channelIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	rows, err := client.SQLDriver.Query(
		fmt.Sprintf("SELECT channel_id, channel_name, thumbnail_medium FROM yt_channels WHERE channel_id IN (%s)", strings.Join(placeholders, ",")),
		args...,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch channels"})
		return
	}
	defer rows.Close()

	var channels []CuratorChannel
	for rows.Next() {
		var ch CuratorChannel
		var thumbnail *string
		if err := rows.Scan(&ch.ChannelID, &ch.ChannelName, &thumbnail); err != nil {
			continue
		}
		if thumbnail != nil {
			ch.Thumbnail = *thumbnail
		}
		ch.Tracked = true
		channels = append(channels, ch)
	}

	if channels == nil {
		channels = []CuratorChannel{}
	}

	c.JSON(http.StatusOK, gin.H{"channels": channels})
}
