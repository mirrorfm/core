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
		// Success, don't retry
		return nil
	}, client.backoff)
	client.backoff.Reset()
	return
}

func (client *retryableDiscogsClient) GetLabelReleases(page, label int) (resp *discogs.LabelReleases, err error) {
	err = backoff.Retry(func() error {
		resp, err = client.Discogs.LabelReleases(label, &discogs.Pagination{
			Sort: "year",
			SortOrder: "desc",
			PerPage: 100,
			Page: page,
		})
		if err != nil {
			return err
		}
		// Success, don't retry
		return nil
	}, client.backoff)
	client.backoff.Reset()
	return
}