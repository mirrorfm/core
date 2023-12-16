package main

import (
	"database/sql"
	"fmt"
	"github.com/irlndts/go-discogs"
	"github.com/pkg/errors"
)

func (client *App) UpdateLabelWithThumbnail(label *discogs.Label) error {
	if len(label.Images) > 0 {
		_, err := client.SQLDriver.Exec(fmt.Sprintf(`
		UPDATE dg_labels
		SET thumbnail_medium = ?
		WHERE label_id = ?
	`), label.Images[0].ResourceURL, label.ID)
		return errors.Wrap(err, "failed to update label with thumbnail")
	}
	return nil
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
		&l.LabelID,
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
		localLabel.LastPage = 1
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
		localLabel.LabelID)
	return errors.Wrap(err, "failed to update label stats")
}

func (client *App) GetNextLabel(lastSuccessChannel int) (LocalLabel, error) {
	l := LocalLabel{}
	selDB := client.SQLDriver.QueryRow(fmt.Sprintf(`
		SELECT
			id,
			label_id
		FROM dg_labels
		WHERE (id > ? or id = 1)
		ORDER BY id = 1, id
		LIMIT 1
	`), lastSuccessChannel)
	err := selDB.Scan(
		&l.ID,
		&l.LabelID,
	)
	return l, errors.Wrap(err, "failed to get next label LabelID")
}
