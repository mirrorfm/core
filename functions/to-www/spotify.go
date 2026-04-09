package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	cachedToken   *spotifyToken
	tokenMu       sync.Mutex
	trackURLRegex = regexp.MustCompile(`(?:open\.spotify\.com/track/|spotify:track:)([a-zA-Z0-9]{22})`)
)

type spotifyToken struct {
	AccessToken string `json:"access_token"`
	ExpiresAt   time.Time
}

type spotifyTrackResponse struct {
	Name       string               `json:"name"`
	Artists    []spotifyArtistBrief `json:"artists"`
	Album      spotifyAlbum         `json:"album"`
	ExternalURLs map[string]string  `json:"external_urls"`
}

type spotifyArtistBrief struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type spotifyAlbum struct {
	Images []spotifyImage `json:"images"`
}

type spotifyImage struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type spotifyArtistsResponse struct {
	Artists []spotifyArtistFull `json:"artists"`
}

type spotifyArtistFull struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Genres []string `json:"genres"`
}

func parseSpotifyTrackURL(rawURL string) (string, error) {
	matches := trackURLRegex.FindStringSubmatch(rawURL)
	if len(matches) < 2 {
		return "", fmt.Errorf("invalid Spotify track URL")
	}
	return matches[1], nil
}

func getSpotifyToken(clientID, clientSecret string) (string, error) {
	tokenMu.Lock()
	defer tokenMu.Unlock()

	if cachedToken != nil && time.Now().Before(cachedToken.ExpiresAt.Add(-60*time.Second)) {
		return cachedToken.AccessToken, nil
	}

	data := url.Values{}
	data.Set("grant_type", "client_credentials")

	req, err := http.NewRequest("POST", "https://accounts.spotify.com/api/token", strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}

	auth := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("spotify token request failed: %s", string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}

	cachedToken = &spotifyToken{
		AccessToken: tokenResp.AccessToken,
		ExpiresAt:   time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
	}

	return cachedToken.AccessToken, nil
}

func getSpotifyTrack(token, trackID string) (*spotifyTrackResponse, error) {
	req, err := http.NewRequest("GET", "https://api.spotify.com/v1/tracks/"+trackID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("track not found")
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("spotify API error: %s", string(body))
	}

	var track spotifyTrackResponse
	if err := json.NewDecoder(resp.Body).Decode(&track); err != nil {
		return nil, err
	}
	return &track, nil
}

func getSpotifyArtists(token string, artistIDs []string) ([]spotifyArtistFull, error) {
	if len(artistIDs) == 0 {
		return nil, nil
	}

	req, err := http.NewRequest("GET", "https://api.spotify.com/v1/artists?ids="+strings.Join(artistIDs, ","), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("spotify artists API error: %s", string(body))
	}

	var result spotifyArtistsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Artists, nil
}

func getRelatedArtists(token string, artistID string) ([]spotifyArtistFull, error) {
	req, err := http.NewRequest("GET", "https://api.spotify.com/v1/artists/"+artistID+"/related-artists", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("spotify related artists API error: %s", string(body))
	}

	var result struct {
		Artists []spotifyArtistFull `json:"artists"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Artists, nil
}

func collectArtistGenres(artists []spotifyArtistFull) []string {
	seen := make(map[string]bool)
	var genres []string
	for _, artist := range artists {
		for _, genre := range artist.Genres {
			if !seen[genre] {
				seen[genre] = true
				genres = append(genres, genre)
			}
		}
	}
	return genres
}

// rankArtistGenres returns genres ordered by relevance:
// - Genres shared by multiple artists score higher
// - Primary artist (first) genres get a boost
// - Within equal scores, earlier position in the artist's list wins
func rankArtistGenres(artists []spotifyArtistFull) []string {
	type genreScore struct {
		name  string
		score float64
		order int // first-seen position for tie-breaking
	}

	scores := make(map[string]*genreScore)
	idx := 0

	for i, artist := range artists {
		// Primary artist gets 2x weight
		weight := 1.0
		if i == 0 {
			weight = 2.0
		}

		for j, genre := range artist.Genres {
			if s, ok := scores[genre]; ok {
				s.score += weight
			} else {
				// Earlier position in artist's genre list = slight boost
				posBonus := 1.0 / float64(j+1) * 0.1
				scores[genre] = &genreScore{
					name:  genre,
					score: weight + posBonus,
					order: idx,
				}
				idx++
			}
		}
	}

	ranked := make([]*genreScore, 0, len(scores))
	for _, s := range scores {
		ranked = append(ranked, s)
	}

	sort.Slice(ranked, func(a, b int) bool {
		if ranked[a].score != ranked[b].score {
			return ranked[a].score > ranked[b].score
		}
		return ranked[a].order < ranked[b].order
	})

	result := make([]string, len(ranked))
	for i, s := range ranked {
		result[i] = s.name
	}
	return result
}
