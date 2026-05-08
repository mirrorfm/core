package main

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/cenkalti/backoff/v4"
	"github.com/irlndts/go-discogs"
	"github.com/pkg/errors"
)

// Sentinel error: surfaced when the upstream Discogs record is deleted/gone
// (404/410). Callers in the Handler use this to advance the cursor past the
// offending row instead of retrying forever (which is what the previous code
// did — see git history for the "stuck cursor on label_id=N" incident).
var ErrNotFound = errors.New("discogs: not found (404/410)")

var (
	retryCode    = strconv.Itoa(http.StatusTooManyRequests)
	notFoundCode = strconv.Itoa(http.StatusNotFound)
	goneCode     = strconv.Itoa(http.StatusGone)
)

// classify maps a go-discogs error into the three categories the retry layer
// needs to distinguish:
//
//	nil                 → success, no retry
//	err (raw)           → transient (429, 5xx, network) — backoff will retry
//	backoff.Permanent() → permanent — backoff stops immediately, caller surfaces
//
// We string-match the message because go-discogs doesn't expose status codes
// on its error type. The match is loose ("contains 429" / "contains 404") and
// is safe in practice given the library's "discogs: <code> <text>" format.
func classify(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()

	// Transient — retry.
	if strings.Contains(msg, retryCode) {
		return err
	}
	if strings.Contains(msg, " 500") || strings.Contains(msg, " 502") ||
		strings.Contains(msg, " 503") || strings.Contains(msg, " 504") {
		return err
	}

	// Permanent: record gone — caller should skip and advance.
	if strings.Contains(msg, notFoundCode) || strings.Contains(msg, goneCode) {
		return backoff.Permanent(ErrNotFound)
	}

	// Other 4xx (auth, malformed, etc.) — permanent, surface as-is so it's
	// loud and gets escalated rather than swallowed.
	return backoff.Permanent(err)
}

func (client *App) GetLabel(label int) (*discogs.Label, error) {
	var resp *discogs.Label
	err := backoff.Retry(func() error {
		var e error
		resp, e = client.Discogs.Label(label)
		return classify(e)
	}, client.Backoff)
	client.Backoff.Reset()
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, errors.Wrapf(err, "GetLabel(%d)", label)
	}
	return resp, nil
}

func (client *App) GetLabelReleases(page, label int) (*discogs.LabelReleases, error) {
	var resp *discogs.LabelReleases
	err := backoff.Retry(func() error {
		var e error
		resp, e = client.Discogs.LabelReleases(label, &discogs.Pagination{
			Sort:      "year",
			SortOrder: "asc",
			PerPage:   25,
			Page:      page,
		})
		return classify(e)
	}, client.Backoff)
	client.Backoff.Reset()
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, errors.Wrapf(err, "GetLabelReleases(label=%d, page=%d)", label, page)
	}
	return resp, nil
}

func (client *App) GetRelease(id int) (*discogs.Release, error) {
	var resp *discogs.Release
	err := backoff.Retry(func() error {
		var e error
		resp, e = client.Discogs.Release(id)
		return classify(e)
	}, client.Backoff)
	client.Backoff.Reset()
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, errors.Wrapf(err, "GetRelease(%d)", id)
	}
	return resp, nil
}
