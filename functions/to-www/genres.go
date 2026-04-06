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
