package main

import (
	"database/sql"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/gin-gonic/gin"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type PaginationParams struct {
	Page    int
	PerPage int
	Sort    string
	Order   string
	Search  string
}

var channelSortColumns = map[string]string{
	"followers": "count_followers",
	"added":     "c.added_datetime",
	"tracks":    "count_tracks",
	"updated":   "last_found_time",
}

var labelSortColumns = map[string]string{
	"followers": "count_followers",
	"added":     "l.added_datetime",
	"tracks":    "count_tracks",
	"updated":   "last_found_time",
}

func parsePaginationParams(c *gin.Context) PaginationParams {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "30"))
	if perPage < 1 {
		perPage = 1
	}
	if perPage > 100 {
		perPage = 100
	}
	sortParam := c.DefaultQuery("sort", "followers")
	order := strings.ToUpper(c.DefaultQuery("order", "DESC"))
	if order != "ASC" && order != "DESC" {
		order = "DESC"
	}
	search := c.DefaultQuery("search", "")

	return PaginationParams{
		Page:    page,
		PerPage: perPage,
		Sort:    sortParam,
		Order:   order,
		Search:  search,
	}
}

type Genre struct {
	Name  string `json:"name" dynamodbav:"genre_name"`
	Count int    `json:"count"`
}

type Entity struct {
	ID              int            `json:"id"`
	FoundTracks     int            `json:"found_tracks"`
	ThumbnailMedium sql.NullString `json:"thumbnail_medium"`
	AddedDatetime   sql.NullTime   `json:"added_datetime"`
	PlaylistId      string         `json:"playlist_id"`
	CountFollowers  int            `json:"count_followers"`
	Genres          []Genre        `json:"genres"`
	LastFoundTime   time.Time      `json:"last_found_time"`
	CountTracks     int            `json:"count_tracks"`
}

type YoutubeChannel struct {
	ChannelId          string       `json:"channel_id"`
	ChannelName        string       `json:"channel_name"`
	TerminatedDatetime sql.NullTime `json:"terminated_datetime"`
	LastUploadDatetime time.Time    `json:"last_upload_datetime"`
	UploadPlaylistId   string       `json:"upload_playlist_id"`
	Entity
}

type DiscogsLabel struct {
	LabelId   string `json:"label_id"`
	LabelName string `json:"label_name"`
	Entity
}

type Event struct {
	Host            string `json:"host"`
	Timestamp       string `json:"timestamp"`
	Added           int    `json:"added"`
	SpotifyPlaylist string `json:"spotify_playlist" dynamodbav:"spotify_playlist"`
	EntityID        string `json:"entity_id" dynamodbav:"entity_id"`
	EntityName      string `json:"entity_name" dynamodbav:"entity_name"`
}

func (c *Client) getYoutubeChannels(orderBy, order string, limit, limitGenres int) (res []YoutubeChannel, err error) {
	query := fmt.Sprintf(`
		SELECT
			c.id, c.channel_id, c.channel_name, c.count_tracks as count_tracks,
			c.last_upload_datetime, c.thumbnail_medium, c.upload_playlist_id,
			c.terminated_datetime, c.added_datetime,
			p.spotify_playlist, p.found_tracks, p.count_followers as count_followers,
			p.last_found_time as last_found_time
		FROM
			yt_channels as c
		INNER JOIN
			yt_playlists p on c.channel_id = p.channel_id
		GROUP BY id ORDER BY %s %s LIMIT ?`, orderBy, order)
	selDB, err := c.SQLDriver.Query(query, limit)
	if err != nil {
		fmt.Println(err.Error())
		return res, err
	}

	var ch YoutubeChannel
	for selDB.Next() {
		err = selDB.Scan(
			&ch.ID,
			&ch.ChannelId,
			&ch.ChannelName,
			&ch.CountTracks,
			&ch.LastUploadDatetime,
			&ch.ThumbnailMedium,
			&ch.UploadPlaylistId,
			&ch.TerminatedDatetime,
			&ch.AddedDatetime,
			&ch.PlaylistId,
			&ch.FoundTracks,
			&ch.CountFollowers,
			&ch.LastFoundTime)
		if err != nil {
			return res, err
		}
		genres, err := c.getEntityGenres(ch.ID, limitGenres)
		if err != nil {
			return res, err
		}
		ch.Genres = genres

		res = append(res, ch)
	}
	return res, nil
}

func (c *Client) getYoutubeChannelsTerminated(orderBy, order string, limit, limitGenres int) (res []YoutubeChannel, err error) {
	query := fmt.Sprintf(`
		SELECT
			c.id, c.channel_id, c.channel_name, c.count_tracks as count_tracks,
			c.last_upload_datetime, c.thumbnail_medium, c.upload_playlist_id,
			c.terminated_datetime, c.added_datetime,
			p.spotify_playlist, p.found_tracks, p.count_followers as count_followers,
			p.last_found_time as last_found_time
		FROM
			yt_channels as c
		INNER JOIN
			yt_playlists p on c.channel_id = p.channel_id
		WHERE
			terminated_datetime IS NOT NULL
		GROUP BY id ORDER BY %s %s LIMIT ?`, orderBy, order)
	selDB, err := c.SQLDriver.Query(query, limit)
	if err != nil {
		fmt.Println(err.Error())
		return res, err
	}

	var ch YoutubeChannel
	for selDB.Next() {
		err = selDB.Scan(
			&ch.ID,
			&ch.ChannelId,
			&ch.ChannelName,
			&ch.CountTracks,
			&ch.LastUploadDatetime,
			&ch.ThumbnailMedium,
			&ch.UploadPlaylistId,
			&ch.TerminatedDatetime,
			&ch.AddedDatetime,
			&ch.PlaylistId,
			&ch.FoundTracks,
			&ch.CountFollowers,
			&ch.LastFoundTime)
		if err != nil {
			return res, err
		}

		genres, err := c.getEntityGenres(ch.ID, limitGenres)
		if err != nil {
			return res, err
		}
		ch.Genres = genres

		res = append(res, ch)
	}
	return res, nil
}

func (c *Client) getEntityGenres(entityId int, limitGenres int) ([]Genre, error) {
	query := fmt.Sprintf(`
		SELECT genre_name, count
		FROM yt_genres
		WHERE yt_channel_id = ?
		ORDER BY count DESC
		LIMIT ?`)

	db, err := c.SQLDriver.Query(query, entityId, limitGenres)
	if err != nil {
		fmt.Println(err.Error())
		return nil, err
	}

	var g Genre
	var genres []Genre

	for db.Next() {
		err = db.Scan(&g.Name, &g.Count)
		if err != nil {
			return nil, err
		}
		genres = append(genres, g)
	}

	return genres, nil
}

func (c *Client) getTableCount(table string) (count *int64, err error) {
	describeTable := &dynamodb.DescribeTableInput{
		TableName: aws.String(table),
	}
	res, err := c.DynamoDB.DescribeTable(describeTable)
	if err != nil {
		return nil, err
	}
	return res.Table.ItemCount, nil
}

func (c *Client) getYoutubeChannel(channelId string) (ch YoutubeChannel, err error) {
	selDB := c.SQLDriver.QueryRow(fmt.Sprintf(`
		SELECT
		    c.id, c.channel_id, c.channel_name, c.count_tracks, c.last_upload_datetime,
		    c.thumbnail_medium, c.upload_playlist_id, c.terminated_datetime, c.added_datetime,
		    p.spotify_playlist, p.found_tracks
		FROM yt_channels as c
		INNER JOIN yt_playlists p on c.channel_id = p.channel_id
		WHERE c.channel_id = ?`), channelId)

	err = selDB.Scan(
		&ch.ID,
		&ch.ChannelId,
		&ch.ChannelName,
		&ch.CountTracks,
		&ch.LastUploadDatetime,
		&ch.ThumbnailMedium,
		&ch.UploadPlaylistId,
		&ch.TerminatedDatetime,
		&ch.AddedDatetime,
		&ch.PlaylistId,
		&ch.FoundTracks)

	return ch, err
}

func (c *Client) getDiscogsLabels(orderBy, order string, limit, limitGenres int) (res []DiscogsLabel, err error) {
	query := fmt.Sprintf(`
		SELECT
			l.id, l.label_id, l.label_name, l.count_tracks as count_tracks,
			l.thumbnail_medium, l.added_datetime,
			p.spotify_playlist, p.found_tracks, p.count_followers as count_followers,
			p.last_found_time as last_found_time
		FROM
			dg_labels as l
		INNER JOIN
			dg_playlists p on l.label_id = p.label_id
		GROUP BY id ORDER BY %s %s LIMIT ?`, orderBy, order)
	selDB, err := c.SQLDriver.Query(query, limit)
	if err != nil {
		fmt.Println(err.Error())
		return res, err
	}

	var la DiscogsLabel
	for selDB.Next() {
		err = selDB.Scan(
			&la.ID,
			&la.LabelId,
			&la.LabelName,
			&la.FoundTracks,
			//&la.LastUploadDatetime,
			&la.ThumbnailMedium,
			&la.AddedDatetime,
			&la.PlaylistId,
			&la.CountTracks,
			&la.CountFollowers,
			&la.LastFoundTime)
		if err != nil {
			return res, err
		}

		genres, err := c.getEntityGenres(la.ID, limitGenres)
		if err != nil {
			return res, err
		}
		la.Genres = genres

		res = append(res, la)
	}
	return res, nil
}

func (c *Client) getYoutubeChannelsPaginated(params PaginationParams, limitGenres int) ([]YoutubeChannel, int, error) {
	sortCol, ok := channelSortColumns[params.Sort]
	if !ok {
		sortCol = "count_followers"
	}
	offset := (params.Page - 1) * params.PerPage
	escapedSearch := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(params.Search)

	// Count query
	countQuery := `SELECT COUNT(DISTINCT c.id) FROM yt_channels as c
		INNER JOIN yt_playlists p on c.channel_id = p.channel_id`
	var countArgs []interface{}
	if escapedSearch != "" {
		countQuery += ` WHERE c.channel_name LIKE ?`
		countArgs = append(countArgs, "%"+escapedSearch+"%")
	}
	var totalCount int
	err := c.SQLDriver.QueryRow(countQuery, countArgs...).Scan(&totalCount)
	if err != nil {
		return nil, 0, err
	}

	// Data query
	query := fmt.Sprintf(`
		SELECT
			c.id, c.channel_id, c.channel_name, c.count_tracks as count_tracks,
			c.last_upload_datetime, c.thumbnail_medium, c.upload_playlist_id,
			c.terminated_datetime, c.added_datetime,
			p.spotify_playlist, p.found_tracks, p.count_followers as count_followers,
			p.last_found_time as last_found_time
		FROM yt_channels as c
		INNER JOIN yt_playlists p on c.channel_id = p.channel_id`)
	var args []interface{}
	if escapedSearch != "" {
		query += ` WHERE c.channel_name LIKE ?`
		args = append(args, "%"+escapedSearch+"%")
	}
	query += fmt.Sprintf(` GROUP BY id ORDER BY %s %s LIMIT ? OFFSET ?`, sortCol, params.Order)
	args = append(args, params.PerPage, offset)

	selDB, err := c.SQLDriver.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}

	var res []YoutubeChannel
	var ids []interface{}
	var ch YoutubeChannel
	for selDB.Next() {
		err = selDB.Scan(
			&ch.ID, &ch.ChannelId, &ch.ChannelName, &ch.CountTracks,
			&ch.LastUploadDatetime, &ch.ThumbnailMedium, &ch.UploadPlaylistId,
			&ch.TerminatedDatetime, &ch.AddedDatetime,
			&ch.PlaylistId, &ch.FoundTracks, &ch.CountFollowers, &ch.LastFoundTime)
		if err != nil {
			return nil, 0, err
		}
		ch.Genres = nil
		res = append(res, ch)
		ids = append(ids, ch.ID)
	}

	// Batch fetch genres
	if len(ids) > 0 {
		genreMap, err := c.getEntityGenresBatch(ids, limitGenres)
		if err != nil {
			return nil, 0, err
		}
		for i := range res {
			if genres, ok := genreMap[res[i].ID]; ok {
				res[i].Genres = genres
			}
		}
	}

	return res, totalCount, nil
}

func (c *Client) getDiscogsLabelsPaginated(params PaginationParams, limitGenres int) ([]DiscogsLabel, int, error) {
	sortCol, ok := labelSortColumns[params.Sort]
	if !ok {
		sortCol = "count_followers"
	}
	offset := (params.Page - 1) * params.PerPage
	escapedSearch := strings.NewReplacer("%", "\\%", "_", "\\_").Replace(params.Search)

	// Count query
	countQuery := `SELECT COUNT(DISTINCT l.id) FROM dg_labels as l
		INNER JOIN dg_playlists p on l.label_id = p.label_id`
	var countArgs []interface{}
	if escapedSearch != "" {
		countQuery += ` WHERE l.label_name LIKE ?`
		countArgs = append(countArgs, "%"+escapedSearch+"%")
	}
	var totalCount int
	err := c.SQLDriver.QueryRow(countQuery, countArgs...).Scan(&totalCount)
	if err != nil {
		return nil, 0, err
	}

	// Data query
	query := fmt.Sprintf(`
		SELECT
			l.id, l.label_id, l.label_name, l.count_tracks as count_tracks,
			l.thumbnail_medium, l.added_datetime,
			p.spotify_playlist, p.found_tracks, p.count_followers as count_followers,
			p.last_found_time as last_found_time
		FROM dg_labels as l
		INNER JOIN dg_playlists p on l.label_id = p.label_id`)
	var args []interface{}
	if escapedSearch != "" {
		query += ` WHERE l.label_name LIKE ?`
		args = append(args, "%"+escapedSearch+"%")
	}
	query += fmt.Sprintf(` GROUP BY id ORDER BY %s %s LIMIT ? OFFSET ?`, sortCol, params.Order)
	args = append(args, params.PerPage, offset)

	selDB, err := c.SQLDriver.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}

	var res []DiscogsLabel
	var ids []interface{}
	var la DiscogsLabel
	for selDB.Next() {
		err = selDB.Scan(
			&la.ID, &la.LabelId, &la.LabelName, &la.FoundTracks,
			&la.ThumbnailMedium, &la.AddedDatetime,
			&la.PlaylistId, &la.CountTracks, &la.CountFollowers, &la.LastFoundTime)
		if err != nil {
			return nil, 0, err
		}
		la.Genres = nil
		res = append(res, la)
		ids = append(ids, la.ID)
	}

	// Batch fetch genres
	if len(ids) > 0 {
		genreMap, err := c.getEntityGenresBatch(ids, limitGenres)
		if err != nil {
			return nil, 0, err
		}
		for i := range res {
			if genres, ok := genreMap[res[i].ID]; ok {
				res[i].Genres = genres
			}
		}
	}

	return res, totalCount, nil
}

func (c *Client) getEntityGenresBatch(ids []interface{}, limitGenres int) (map[int][]Genre, error) {
	placeholders := make([]string, len(ids))
	for i := range ids {
		placeholders[i] = "?"
	}
	query := fmt.Sprintf(`
		SELECT yt_channel_id, genre_name, count
		FROM yt_genres
		WHERE yt_channel_id IN (%s)
		ORDER BY yt_channel_id, count DESC`, strings.Join(placeholders, ","))

	rows, err := c.SQLDriver.Query(query, ids...)
	if err != nil {
		return nil, err
	}

	result := make(map[int][]Genre)
	var entityId int
	var g Genre
	for rows.Next() {
		err = rows.Scan(&entityId, &g.Name, &g.Count)
		if err != nil {
			return nil, err
		}
		if len(result[entityId]) < limitGenres {
			result[entityId] = append(result[entityId], g)
		}
	}
	return result, nil
}

func (c *Client) getDistinctGenres(table string) ([]string, error) {
	query := fmt.Sprintf(`SELECT DISTINCT genre_name FROM %s ORDER BY genre_name`, table)
	rows, err := c.SQLDriver.Query(query)
	if err != nil {
		return nil, err
	}
	var genres []string
	var name string
	for rows.Next() {
		err = rows.Scan(&name)
		if err != nil {
			return nil, err
		}
		genres = append(genres, name)
	}
	return genres, nil
}

func (c *Client) getDiscogsLabel(labelId string) (la DiscogsLabel, err error) {
	selDB := c.SQLDriver.QueryRow(fmt.Sprintf(`
		SELECT
		    l.id, l.label_id, l.label_name, l.count_tracks, l.thumbnail_medium, l.added_datetime,
		    p.spotify_playlist, p.found_tracks
		FROM dg_labels as l
		INNER JOIN dg_playlists p on l.label_id = p.label_id
		WHERE l.label_id = ?`), labelId)

	err = selDB.Scan(
		&la.ID,
		&la.LabelId,
		&la.LabelName,
		&la.FoundTracks,
		&la.ThumbnailMedium,
		&la.AddedDatetime,
		&la.PlaylistId,
		&la.CountTracks)

	return la, err
}

func (c *Client) getEventsFromType(count int, entity string, ch chan []Event, wg *sync.WaitGroup) error {
	defer (*wg).Done()

	oneDayAgo := time.Now().Local().Add(-time.Hour * 24).Unix()

	var ev []Event
	queryInput := &dynamodb.QueryInput{
		KeyConditions: map[string]*dynamodb.Condition{
			"host": {
				ComparisonOperator: aws.String("EQ"),
				AttributeValueList: []*dynamodb.AttributeValue{
					{
						S: aws.String(entity),
					},
				},
			},
			"timestamp": {
				ComparisonOperator: aws.String("GE"),
				AttributeValueList: []*dynamodb.AttributeValue{
					{
						N: aws.String(strconv.FormatInt(oneDayAgo, 10)),
					},
				},
			},
		},
		Limit:            aws.Int64(int64(count)),
		ScanIndexForward: aws.Bool(false),
		TableName:        aws.String(c.DynamoDBEventsTable),
	}

	result, err := c.DynamoDB.Query(queryInput)
	if err != nil {
		fmt.Println("Query API call failed:")
		fmt.Println(err.Error())
		return err
	}

	err = dynamodbattribute.UnmarshalListOfMaps(result.Items, &ev)
	if err != nil {
		fmt.Println("Got error unmarshalling events")
		fmt.Println(err.Error())
		return err
	}
	ch <- ev
	return nil
}

func (c *Client) getEvents(count int) (events []Event, err error) {
	ch := make(chan []Event)
	var wg sync.WaitGroup

	wg.Add(1)
	go c.getEventsFromType(count, "yt", ch, &wg)

	wg.Add(1)
	go c.getEventsFromType(count, "dg", ch, &wg)

	go func() {
		wg.Wait()
		close(ch)
	}()

	for entityEvents := range ch {
		events = append(events, entityEvents...)
	}

	// Mix both YT and DG events
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp > events[j].Timestamp
	})

	return events, nil
}
