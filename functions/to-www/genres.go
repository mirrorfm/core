package main

import (
	"net/http"

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
	rows, err := client.SQLDriver.Query(`
		SELECT a.genre_name, b.genre_name, COUNT(DISTINCT a.yt_channel_id) as shared
		FROM yt_genres a
		JOIN yt_genres b ON a.yt_channel_id = b.yt_channel_id AND a.genre_name < b.genre_name
		WHERE a.count >= 3 AND b.count >= 3
		GROUP BY a.genre_name, b.genre_name
		HAVING shared >= 5
		ORDER BY shared DESC
		LIMIT 500
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch genre graph"})
		return
	}
	defer rows.Close()

	nodesSet := make(map[string]bool)
	var links []GenreLink
	for rows.Next() {
		var l GenreLink
		if err := rows.Scan(&l.Source, &l.Target, &l.Weight); err != nil {
			continue
		}
		links = append(links, l)
		nodesSet[l.Source] = true
		nodesSet[l.Target] = true
	}

	var nodes []GenreCount
	if len(nodesSet) > 0 {
		// Get counts for all nodes
		placeholders := ""
		args := make([]interface{}, 0, len(nodesSet))
		for name := range nodesSet {
			if placeholders != "" {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, name)
		}
		nodeRows, err := client.SQLDriver.Query(
			"SELECT genre_name, SUM(count) as total FROM yt_genres WHERE genre_name IN ("+placeholders+") GROUP BY genre_name",
			args...,
		)
		if err == nil {
			defer nodeRows.Close()
			for nodeRows.Next() {
				var g GenreCount
				if err := nodeRows.Scan(&g.Name, &g.Count); err != nil {
					continue
				}
				nodes = append(nodes, g)
			}
		}
	}

	if links == nil {
		links = []GenreLink{}
	}
	if nodes == nil {
		nodes = []GenreCount{}
	}

	c.JSON(http.StatusOK, gin.H{"nodes": nodes, "links": links})
}
