package main

import (
	"database/sql"
	"fmt"
	"github.com/aws/aws-lambda-go/lambda"
	_ "github.com/go-sql-driver/mysql"
	webPlayer "github.com/mirrorfm/spotify-webplayer-token/app"
	api "github.com/mirrorfm/unofficial-spotify-api/app"
	"github.com/pkg/errors"
	"os"
	"strings"
)

type App struct {
	SQLDriver *sql.DB
}

func getApp() (App, error) {
	// MySQL
	dbHost := os.Getenv("DB_HOST")
	dbUser := os.Getenv("DB_USERNAME")
	dbPass := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	sqlDriver, err := sql.Open("mysql", dbUser+":"+dbPass+"@tcp("+dbHost+")/"+dbName+"?parseTime=true")
	if err != nil {
		return App{}, errors.Wrap(err, "failed to set up DB client")
	}

	return App{
		SQLDriver: sqlDriver,
	}, nil
}

func Handler() error {
	app, err := getApp()
	if err != nil {
		return errors.Wrap(err, "failed to start up app")
	}

	token, err := webPlayer.GetAccessTokenFromEnv()
	if err != nil && token != nil && !token.IsAnonymous {
		os.Exit(1)
	}

	userId, exists := api.GetUserIdFromEnv()
	if !exists {
		os.Exit(1)
	}

	limit := 150
	playlistsByFollowers, err := app.GetPlaylistsSortedByTotalFollowers(limit)
	if err != nil {
		os.Exit(1)
	}
	playlistsByAddedDatetime, err := app.GetPlaylistsSortedByAddedDatetime(limit)
	if err != nil {
		os.Exit(1)
	}
	mixedOrderPlaylist := mergeUnique(playlistsByFollowers, playlistsByAddedDatetime)

	rootList := api.RootListResponse{}

	for expectedPosition, playlist := range mixedOrderPlaylist {
		if rootList.Revision == "" {
			// request RootList on first run and after every successful change
			res, status, err := api.GetRootList(token.AccessToken, userId)
			if err != nil {
				os.Exit(1)
			}
			rootList = *res
			fmt.Printf("GET RootList: %d\n", status)
		}

		sortOperations := GenerateSortOperations(rootList.Contents.Items, playlist, expectedPosition)
		if sortOperations != nil {
			_, status, err := api.PostRootListChanges([]api.DeltaOps{*sortOperations}, rootList.Revision, token.AccessToken, userId)
			if err != nil {
				os.Exit(1)
			}
			fmt.Printf("POST RootListChanges: %d\n", status)

			rootList.Revision = ""
		}
	}

	return nil
}

func mergeUnique(pl1, pl2 []string) []string {
	check := make(map[string]bool)
	var playlists []string

	l := min(len(pl1), len(pl2))

	for i := 0; i < l; i++ {
		appendUnique(pl1[i], check, &playlists)
		appendUnique(pl2[i], check, &playlists)
	}

	return playlists
}

func appendUnique(pl string, check map[string]bool, mixedPl *[]string) {
	if _, ok := check[pl]; !ok {
		check[pl] = true
		*mixedPl = append(*mixedPl, pl)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func findPlaylistCurrentPosition(playlistId string, contentItems []api.ContentsItem) (int, bool) {
	for idx, contentItem := range contentItems {
		if strings.Contains(contentItem.Uri, playlistId) {
			return idx, true
		}
	}
	return 0, false
}

func GenerateSortOperations(contentItems []api.ContentsItem, playlistId string, expectedPosition int) *api.DeltaOps {
	currentPosition, found := findPlaylistCurrentPosition(playlistId, contentItems)
	if found && currentPosition != expectedPosition {
		return &api.DeltaOps{
			Kind: "MOV",
			Mov: api.OpsMov{
				FromIndex: currentPosition,
				Length:    1,
				ToIndex:   expectedPosition,
			},
		}
	}

	return nil
}

func (client *App) GetPlaylistsSortedByAddedDatetime(limit int) ([]string, error) {
	var playlists []string
	db, err := client.SQLDriver.Query(fmt.Sprintf(`
		SELECT spotify_playlist FROM
		(
				SELECT
					   yt_playlists.spotify_playlist as spotify_playlist,
					   yt_channels.added_datetime as added_datetime
				FROM yt_playlists JOIN yt_channels on yt_playlists.channel_id = yt_channels.channel_id
				UNION ALL
				SELECT
					   dg_playlists.spotify_playlist as spotify_playlist,
					   dg_labels.added_datetime as added_datetime
				FROM dg_playlists JOIN dg_labels on dg_playlists.label_id = dg_labels.label_id
		) T1
		ORDER BY added_datetime DESC
		LIMIT ?
	`), limit)
	if err != nil {
		fmt.Println(err.Error())
		return playlists, err
	}
	var playlistId string
	for db.Next() {
		err = db.Scan(&playlistId)
		if err != nil {
			return playlists, err
		}
		playlists = append(playlists, playlistId)
	}
	return playlists, nil
}

func (client *App) GetPlaylistsSortedByTotalFollowers(limit int) ([]string, error) {
	var playlists []string
	db, err := client.SQLDriver.Query(fmt.Sprintf(`
		SELECT spotify_playlist FROM
		(
			SELECT spotify_playlist, count_followers FROM yt_playlists
			UNION ALL
			SELECT spotify_playlist, count_followers FROM dg_playlists
		) T1
		ORDER BY count_followers DESC
		LIMIT ?
	`), limit)
	if err != nil {
		fmt.Println(err.Error())
		return playlists, err
	}
	var playlistId string
	for db.Next() {
		err = db.Scan(&playlistId)
		if err != nil {
			return playlists, err
		}
		playlists = append(playlists, playlistId)
	}
	return playlists, nil
}

func main() {
	if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
		lambda.Start(Handler)
	} else {
		err := Handler()
		if err != nil {
			fmt.Println(err.Error())
		}
	}
}
