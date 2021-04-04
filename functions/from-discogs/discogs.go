package main

import (
	"github.com/cenkalti/backoff/v4"
	"github.com/irlndts/go-discogs"
	"github.com/pkg/errors"
	"net/http"
	"strconv"
	"strings"
)

var (
	RetryCode = strconv.Itoa(http.StatusTooManyRequests)
)

func (client *App) GetRelease(id int) (resp *discogs.Release, err error) {
	err = backoff.Retry(func() error {
		resp, err = client.Discogs.Release(id)
		if err != nil && strings.Contains(err.Error(), RetryCode) {
			return err // retry
		}
		return nil
	}, client.Backoff)
	client.Backoff.Reset()
	if resp == nil {
		err = errors.New("retries exceeded")
	}
	return
}

func (client *App) GetLabelReleases(page, label int) (resp *discogs.LabelReleases, err error) {
	err = backoff.Retry(func() error {
		resp, err = client.Discogs.LabelReleases(label, &discogs.Pagination{
			Sort:      "year",
			SortOrder: "asc",
			PerPage:   25,
			Page:      page,
		})
		if err != nil && strings.Contains(err.Error(), RetryCode) {
			return err // retry
		}
		return nil
	}, client.Backoff)
	client.Backoff.Reset()
	if resp == nil {
		err = errors.New("retries exceeded")
	}
	return
}

func (client *App) GetLabel(label int) (resp *discogs.Label, err error) {
	err = backoff.Retry(func() error {
		resp, err = client.Discogs.Label(label)
		if err != nil && strings.Contains(err.Error(), RetryCode) {
			return err // retry
		}
		return nil
	}, client.Backoff)
	client.Backoff.Reset()
	if resp == nil {
		err = errors.New("retries exceeded")
	}
	return
}
