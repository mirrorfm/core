package main

import (
	"github.com/cenkalti/backoff/v4"
	"github.com/irlndts/go-discogs"
)

func (client *retryableDiscogsClient) GetRelease(id int) (resp *discogs.Release, err error) {
	err = backoff.Retry(func() error {
		resp, err = client.Discogs.Release(id)
		if err != nil {
			return err
		}
		return nil
	}, client.backoff)
	client.backoff.Reset()
	return
}

func (client *retryableDiscogsClient) GetLabelReleases(page, label int) (resp *discogs.LabelReleases, err error) {
	err = backoff.Retry(func() error {
		resp, err = client.Discogs.LabelReleases(label, &discogs.Pagination{
			Sort: "year",
			SortOrder: "asc",
			PerPage: 100,
			Page: page,
		})
		if err != nil {
			return err
		}
		return nil
	}, client.backoff)
	client.backoff.Reset()
	return
}

func (client *retryableDiscogsClient) GetLabel(label int) (resp *discogs.Label, err error) {
	err = backoff.Retry(func() error {
		resp, err = client.Discogs.Label(label)
		if err != nil {
			return err
		}
		return nil
	}, client.backoff)
	client.backoff.Reset()
	return
}