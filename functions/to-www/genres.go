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
	// Fetch all genre-channel pairs for eligible genres
	rows, err := client.SQLDriver.Query(`
		SELECT g.yt_channel_id, g.genre_name, g.count
		FROM yt_genres g
		INNER JOIN (
			SELECT genre_name FROM yt_genres GROUP BY genre_name HAVING COUNT(DISTINCT yt_channel_id) >= 10
		) e ON g.genre_name = e.genre_name
		WHERE g.count >= 3
		ORDER BY g.yt_channel_id, g.count DESC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch genre data"})
		return
	}
	defer rows.Close()

	// Build: channel → top 3 genres only
	type genreEntry struct {
		name  string
		count int
	}
	channelAll := make(map[int][]genreEntry)
	genreTotals := make(map[string]int)

	for rows.Next() {
		var channelID int
		var genre string
		var count int
		if err := rows.Scan(&channelID, &genre, &count); err != nil {
			continue
		}
		channelAll[channelID] = append(channelAll[channelID], genreEntry{genre, count})
		genreTotals[genre] += count
	}

	// Keep only top 3 per channel
	channelGenres := make(map[int][]string)
	for chID, entries := range channelAll {
		limit := 3
		if len(entries) < limit {
			limit = len(entries)
		}
		for _, e := range entries[:limit] {
			channelGenres[chID] = append(channelGenres[chID], e.name)
		}
	}

	// Compute co-occurrence in memory
	type pair struct{ a, b string }
	cooccur := make(map[pair]int)
	for _, genres := range channelGenres {
		for i := 0; i < len(genres); i++ {
			for j := i + 1; j < len(genres); j++ {
				a, b := genres[i], genres[j]
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

type ChannelNode struct {
	ID        string `json:"id"`
	Name      string `json:"channel_name"`
	Thumbnail string `json:"thumbnail"`
	Followers int    `json:"followers"`
	Genres    []string `json:"genres"`
}

type ChannelLink struct {
	Source       string   `json:"source"`
	Target       string   `json:"target"`
	SharedGenres []string `json:"shared_genres"`
	Weight       int      `json:"weight"`
}

func (client *Client) handleChannelGraph(c *gin.Context) {
	// Fetch channels with their top 3 genres
	rows, err := client.SQLDriver.Query(`
		SELECT c.channel_id, c.channel_name, c.thumbnail_medium, COALESCE(p.count_followers, 0) as followers
		FROM yt_channels c
		LEFT JOIN yt_playlists p ON c.channel_id = p.channel_id
		WHERE c.terminated_datetime IS NULL AND p.found_tracks > 0
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch channels"})
		return
	}
	defer rows.Close()

	type channelInfo struct {
		id        string
		name      string
		thumbnail string
		followers int
	}
	var channels []channelInfo
	channelIDs := make(map[string]int) // channel_id → index

	for rows.Next() {
		var ch channelInfo
		var thumb *string
		if err := rows.Scan(&ch.id, &ch.name, &thumb, &ch.followers); err != nil {
			continue
		}
		if thumb != nil {
			ch.thumbnail = *thumb
		}
		channelIDs[ch.id] = len(channels)
		channels = append(channels, ch)
	}

	// Fetch top 3 genres per channel (by MySQL internal id)
	genreRows, err := client.SQLDriver.Query(`
		SELECT c.channel_id, g.genre_name
		FROM yt_genres g
		JOIN yt_channels c ON c.id = g.yt_channel_id
		WHERE c.terminated_datetime IS NULL
		ORDER BY g.yt_channel_id, g.count DESC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch channel genres"})
		return
	}
	defer genreRows.Close()

	channelGenres := make(map[string][]string)
	genreCount := make(map[string]int)
	var lastChID string
	for genreRows.Next() {
		var chID, genre string
		if err := genreRows.Scan(&chID, &genre); err != nil {
			continue
		}
		if chID != lastChID {
			genreCount[chID] = 0
			lastChID = chID
		}
		genreCount[chID]++
		if genreCount[chID] <= 3 {
			channelGenres[chID] = append(channelGenres[chID], genre)
		}
	}

	// Build genre → channels index
	genreToChannels := make(map[string][]string)
	for chID, genres := range channelGenres {
		for _, g := range genres {
			genreToChannels[g] = append(genreToChannels[g], chID)
		}
	}

	// Compute channel similarity: shared genres >= 2
	type chPair struct{ a, b string }
	pairGenres := make(map[chPair][]string)

	for genre, chs := range genreToChannels {
		for i := 0; i < len(chs); i++ {
			for j := i + 1; j < len(chs); j++ {
				a, b := chs[i], chs[j]
				if a > b {
					a, b = b, a
				}
				p := chPair{a, b}
				pairGenres[p] = append(pairGenres[p], genre)
			}
		}
	}

	// Filter to pairs with 2+ shared genres, build links
	connectedChannels := make(map[string]bool)
	var chLinks []ChannelLink
	for p, genres := range pairGenres {
		if len(genres) >= 2 {
			// Deduplicate genres
			seen := make(map[string]bool)
			var unique []string
			for _, g := range genres {
				if !seen[g] {
					seen[g] = true
					unique = append(unique, g)
				}
			}
			chLinks = append(chLinks, ChannelLink{
				Source: p.a, Target: p.b, SharedGenres: unique, Weight: len(unique),
			})
			connectedChannels[p.a] = true
			connectedChannels[p.b] = true
		}
	}

	// Sort by weight desc, limit
	sort.Slice(chLinks, func(i, j int) bool { return chLinks[i].Weight > chLinks[j].Weight })
	if len(chLinks) > 300 {
		chLinks = chLinks[:300]
		connectedChannels = make(map[string]bool)
		for _, l := range chLinks {
			connectedChannels[l.Source] = true
			connectedChannels[l.Target] = true
		}
	}

	// Build nodes for connected channels only
	var chNodes []ChannelNode
	for _, ch := range channels {
		if connectedChannels[ch.id] {
			chNodes = append(chNodes, ChannelNode{
				ID:        ch.id,
				Name:      ch.name,
				Thumbnail: ch.thumbnail,
				Followers: ch.followers,
				Genres:    channelGenres[ch.id],
			})
		}
	}

	if chLinks == nil {
		chLinks = []ChannelLink{}
	}
	if chNodes == nil {
		chNodes = []ChannelNode{}
	}

	c.JSON(http.StatusOK, gin.H{"nodes": chNodes, "links": chLinks})
}
