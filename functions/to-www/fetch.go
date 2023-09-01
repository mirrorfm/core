package main

import (
	"database/sql"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"sort"
	"strconv"
	"sync"
	"time"
)

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
