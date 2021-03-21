package main

import (
	"database/sql"
	"fmt"
	"github.com/irlndts/go-discogs"
	"github.com/pkg/errors"
	"time"
)

func (client *App) UpdateLabelWithAddedDatetime(label *discogs.Label) error {
	_, err := client.SQLDriver.Exec(fmt.Sprintf(`
		UPDATE dg_labels
		SET
			label_name = ?,
			added_datetime = ?
		WHERE label_id = ?
	`), label.Name, time.Now(), label.ID)
	return errors.Wrap(err, "failed to update label with added datetime")
}

func (client *App) GetLabelInfo(labelId int) (LocalLabel, error) {
	l := LocalLabel{
		MasterReleasesCache: map[int]discogs.Release{},
	}
	selDB := client.SQLDriver.QueryRow(fmt.Sprintf(`
		SELECT
			label_id,
			highest_dg_release,
			label_releases,
			label_tracks,
			last_page,
			did_init
		FROM dg_labels
		WHERE label_id = ?
	`), labelId)
	err := selDB.Scan(
		&l.ID,
		&l.HighestReleaseID,
		&l.LabelReleases,
		&l.LabelTracks,
		&l.LastPage,
		&l.DidInit,
	)
	return l, errors.Wrap(err, "failed to get label info")
}

func (client *App) UpdateLabelWithStats(localLabel LocalLabel, isLastPage bool) error {
	if !localLabel.DidInit.Bool {
		didInit := localLabel.LastPage == localLabel.MaxPages
		nb := sql.NullBool{}
		err := nb.Scan(didInit)
		if err != nil {
			return err
		}
		localLabel.DidInit = nb
	}
	if isLastPage {
		localLabel.LastPage = 0
	}
	_, err := client.SQLDriver.Exec(fmt.Sprintf(`
		UPDATE dg_labels
		SET
			highest_dg_release = ?,
			label_releases = ?,
			label_tracks = ?,
			last_page = ?,
			did_init = ?
		WHERE label_id = ?;
	`),
	localLabel.HighestReleaseID,
	localLabel.LabelReleases,
	localLabel.LabelTracks,
	localLabel.LastPage,
	localLabel.DidInit,
	localLabel.ID)
	return errors.Wrap(err, "failed to update label stats")
}