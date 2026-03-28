package main

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/gin-gonic/gin"
)

type AnalyzeRequest struct {
	URL string `json:"url" binding:"required"`
}

type TrackInfo struct {
	Name       string   `json:"name"`
	Artist     string   `json:"artist"`
	Image      string   `json:"image"`
	Genres     []string `json:"genres"`
	SpotifyURL string   `json:"spotify_url"`
}

type ChannelMatch struct {
	ChannelID      string  `json:"channel_id"`
	ChannelName    string  `json:"channel_name"`
	Thumbnail      string  `json:"thumbnail"`
	PlaylistID     string  `json:"playlist_id"`
	Followers      int     `json:"followers"`
	FoundTracks    int     `json:"found_tracks"`
	TotalTracks    int     `json:"total_tracks"`
	Score          float64 `json:"score"`
	MatchingGenres []string `json:"matching_genres"`
	TopGenres      []Genre  `json:"top_genres"`
}

type InterestRequest struct {
	Email      string   `json:"email" binding:"required"`
	TrackURL   string   `json:"track_url" binding:"required"`
	ChannelIDs []string `json:"channel_ids" binding:"required"`
}

const matchLimit = 20

func (client *Client) handleAnalyze(c *gin.Context) {
	var req AnalyzeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "URL is required"})
		return
	}

	trackID, err := parseSpotifyTrackURL(req.URL)
	if err != nil {
		c.JSON(400, gin.H{"error": "Invalid Spotify track URL. Please paste a link like https://open.spotify.com/track/..."})
		return
	}

	token, err := getSpotifyToken(client.SpotifyClientID, client.SpotifyClientSecret)
	if err != nil {
		fmt.Println("Spotify token error:", err)
		c.JSON(500, gin.H{"error": "Failed to connect to Spotify"})
		return
	}

	track, err := getSpotifyTrack(token, trackID)
	if err != nil {
		if err.Error() == "track not found" {
			c.JSON(404, gin.H{"error": "Track not found on Spotify"})
			return
		}
		fmt.Println("Spotify track error:", err)
		c.JSON(500, gin.H{"error": "Failed to fetch track from Spotify"})
		return
	}

	// Get artist IDs
	artistIDs := make([]string, len(track.Artists))
	artistNames := make([]string, len(track.Artists))
	for i, a := range track.Artists {
		artistIDs[i] = a.ID
		artistNames[i] = a.Name
	}

	// Get full artist info with genres
	artists, err := getSpotifyArtists(token, artistIDs)
	if err != nil {
		fmt.Println("Spotify artists error:", err)
		c.JSON(500, gin.H{"error": "Failed to fetch artist info from Spotify"})
		return
	}

	genres := collectArtistGenres(artists)

	// Build track info
	var imageURL string
	if len(track.Album.Images) > 0 {
		imageURL = track.Album.Images[0].URL
	}

	trackInfo := TrackInfo{
		Name:       track.Name,
		Artist:     strings.Join(artistNames, ", "),
		Image:      imageURL,
		Genres:     genres,
		SpotifyURL: track.ExternalURLs["spotify"],
	}

	// Find matching channels
	var matches []ChannelMatch
	if len(genres) > 0 {
		matches, err = client.findMatchingChannels(genres)
		if err != nil {
			fmt.Println("Matching error:", err)
			c.JSON(500, gin.H{"error": "Failed to find matching channels"})
			return
		}
	}

	if matches == nil {
		matches = []ChannelMatch{}
	}

	c.JSON(200, gin.H{
		"track":   trackInfo,
		"matches": matches,
	})
}

func (client *Client) findMatchingChannels(genres []string) ([]ChannelMatch, error) {
	// Build the IN clause placeholders and args
	placeholders := make([]string, len(genres))
	args := make([]interface{}, 0, len(genres)*2+1)
	for i, g := range genres {
		placeholders[i] = "?"
		args = append(args, g)
	}
	inClause := strings.Join(placeholders, ",")

	// Same genre args needed twice (for SUM CASE and GROUP_CONCAT)
	for _, g := range genres {
		args = append(args, g)
	}

	query := fmt.Sprintf(`
		SELECT
			c.id, c.channel_id, c.channel_name, c.thumbnail_medium,
			p.spotify_playlist, p.count_followers, p.found_tracks, c.count_tracks,
			SUM(CASE WHEN g.genre_name IN (%s) THEN g.count ELSE 0 END) as matching_count,
			SUM(g.count) as total_count,
			GROUP_CONCAT(CASE WHEN g.genre_name IN (%s) THEN g.genre_name END SEPARATOR ',') as matching_genres_csv
		FROM yt_channels c
		INNER JOIN yt_playlists p ON c.channel_id = p.channel_id
		INNER JOIN yt_genres g ON c.id = g.yt_channel_id
		WHERE p.found_tracks > 0
		GROUP BY c.id
		HAVING matching_count > 0
		ORDER BY (matching_count / total_count) DESC, p.count_followers DESC
		LIMIT ?
	`, inClause, inClause)

	args = append(args, matchLimit)

	rows, err := client.SQLDriver.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []ChannelMatch
	var ids []interface{}
	idToIndex := make(map[int]int)

	for rows.Next() {
		var (
			id               int
			channelID        string
			channelName      string
			thumbnail        sql.NullString
			playlistID       string
			followers        int
			foundTracks      int
			totalTracks      int
			matchingCount    float64
			totalCount       float64
			matchingGenreCSV sql.NullString
		)

		err = rows.Scan(&id, &channelID, &channelName, &thumbnail,
			&playlistID, &followers, &foundTracks, &totalTracks,
			&matchingCount, &totalCount, &matchingGenreCSV)
		if err != nil {
			return nil, err
		}

		score := 0.0
		if totalCount > 0 {
			score = matchingCount / totalCount
		}

		var matchingGenres []string
		if matchingGenreCSV.Valid && matchingGenreCSV.String != "" {
			// Deduplicate since GROUP_CONCAT may repeat genres
			seen := make(map[string]bool)
			for _, g := range strings.Split(matchingGenreCSV.String, ",") {
				g = strings.TrimSpace(g)
				if g != "" && !seen[g] {
					seen[g] = true
					matchingGenres = append(matchingGenres, g)
				}
			}
		}

		match := ChannelMatch{
			ChannelID:      channelID,
			ChannelName:    channelName,
			Thumbnail:      thumbnail.String,
			PlaylistID:     playlistID,
			Followers:      followers,
			FoundTracks:    foundTracks,
			TotalTracks:    totalTracks,
			Score:          score,
			MatchingGenres: matchingGenres,
		}

		idToIndex[id] = len(matches)
		ids = append(ids, id)
		matches = append(matches, match)
	}

	// Batch fetch top genres per channel (reuse existing function)
	if len(ids) > 0 {
		genreMap, err := client.getEntityGenresBatch(ids, 6)
		if err != nil {
			return nil, err
		}
		for id, genres := range genreMap {
			if idx, ok := idToIndex[id]; ok {
				matches[idx].TopGenres = genres
			}
		}
	}

	return matches, nil
}

func (client *Client) handleInterest(c *gin.Context) {
	var req InterestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "Email, track URL, and at least one channel ID are required"})
		return
	}

	if len(req.ChannelIDs) == 0 {
		c.JSON(400, gin.H{"error": "At least one channel ID is required"})
		return
	}

	// Build channel IDs as a DynamoDB string set
	channelIDValues := make([]*dynamodb.AttributeValue, len(req.ChannelIDs))
	for i, id := range req.ChannelIDs {
		channelIDValues[i] = &dynamodb.AttributeValue{S: aws.String(id)}
	}

	_, err := client.DynamoDB.PutItem(&dynamodb.PutItemInput{
		TableName: aws.String(client.DynamoDBInterestsTable),
		Item: map[string]*dynamodb.AttributeValue{
			"email":       {S: aws.String(req.Email)},
			"track_url":   {S: aws.String(req.TrackURL)},
			"channel_ids": {SS: aws.StringSlice(req.ChannelIDs)},
			"timestamp":   {N: aws.String(strconv.FormatInt(time.Now().Unix(), 10))},
		},
	})
	if err != nil {
		fmt.Println("DynamoDB interest save error:", err)
		c.JSON(500, gin.H{"error": "Failed to save interest"})
		return
	}

	c.JSON(200, gin.H{"success": true})
}
