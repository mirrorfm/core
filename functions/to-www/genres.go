package main

import (
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
)

type GenreCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func (client *Client) handleGenres(c *gin.Context) {
	rows, err := client.SQLDriver.Query(`
		SELECT genre_name, SUM(count) as total
		FROM yt_genres
		GROUP BY genre_name
		ORDER BY total DESC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch genres"})
		return
	}
	defer rows.Close()

	var genres []GenreCount
	for rows.Next() {
		var g GenreCount
		if err := rows.Scan(&g.Name, &g.Count); err != nil {
			continue
		}
		genres = append(genres, g)
	}
	if genres == nil {
		genres = []GenreCount{}
	}

	c.JSON(http.StatusOK, gin.H{"genres": genres})
}

type GenreLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Weight int    `json:"weight"`
}

func (client *Client) handleGenresGraph(c *gin.Context) {
	// Fetch all genre-channel pairs in one query, compute co-occurrence in memory
	rows, err := client.SQLDriver.Query(`
		SELECT yt_channel_id, genre_name, count FROM yt_genres WHERE count >= 3
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch genre data"})
		return
	}
	defer rows.Close()

	// Build: channel → list of genres, and genre → total count
	channelGenres := make(map[int][]string)
	genreTotals := make(map[string]int)
	genreChannelCount := make(map[string]int)

	for rows.Next() {
		var channelID int
		var genre string
		var count int
		if err := rows.Scan(&channelID, &genre, &count); err != nil {
			continue
		}
		channelGenres[channelID] = append(channelGenres[channelID], genre)
		genreTotals[genre] += count
		genreChannelCount[genre]++
	}

	// Filter: only genres on 10+ channels
	eligible := make(map[string]bool)
	for genre, chCount := range genreChannelCount {
		if chCount >= 10 {
			eligible[genre] = true
		}
	}

	// Compute co-occurrence in memory
	type pair struct{ a, b string }
	cooccur := make(map[pair]int)
	for _, genres := range channelGenres {
		// Only eligible genres
		var filtered []string
		for _, g := range genres {
			if eligible[g] {
				filtered = append(filtered, g)
			}
		}
		for i := 0; i < len(filtered); i++ {
			for j := i + 1; j < len(filtered); j++ {
				a, b := filtered[i], filtered[j]
				if a > b {
					a, b = b, a
				}
				cooccur[pair{a, b}]++
			}
		}
	}

	// Build links (threshold 20+ shared channels)
	nodesSet := make(map[string]bool)
	var links []GenreLink
	for p, count := range cooccur {
		if count >= 20 {
			links = append(links, GenreLink{Source: p.a, Target: p.b, Weight: count})
			nodesSet[p.a] = true
			nodesSet[p.b] = true
		}
	}

	// Sort by weight desc, limit 200
	sort.Slice(links, func(i, j int) bool { return links[i].Weight > links[j].Weight })
	if len(links) > 200 {
		links = links[:200]
		nodesSet = make(map[string]bool)
		for _, l := range links {
			nodesSet[l.Source] = true
			nodesSet[l.Target] = true
		}
	}

	var nodes []GenreCount
	for name := range nodesSet {
		nodes = append(nodes, GenreCount{Name: name, Count: genreTotals[name]})
	}

	if links == nil {
		links = []GenreLink{}
	}
	if nodes == nil {
		nodes = []GenreCount{}
	}

	c.JSON(http.StatusOK, gin.H{"nodes": nodes, "links": links})
}
