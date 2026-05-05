// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package importer

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"iter"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"codeberg.org/readeck/readeck/internal/db/types"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

var (
	_ ImportWorker     = (*pocketFileAdapter)(nil)
	_ BookmarkEnhancer = (*pocketBookmarkItem)(nil)
)

type pocketFileAdapter struct {
	csvBaseAdapter[pocketEntry, *pocketBookmarkItem]
}

type pocketBookmarkItem struct {
	Link       string        `json:"url"`
	Title      string        `json:"title"`
	Labels     types.Strings `json:"labels"`
	IsArchived bool          `json:"is_archived"`
	Created    time.Time     `json:"created"`
}

type pocketEntry struct {
	Title     string `csv:"title"      case:"ignore"`
	URL       string `csv:"url"        case:"ignore"`
	TimeAdded string `csv:"time_added" case:"ignore"`
	Tags      string `csv:"tags"       case:"ignore"`
	Status    string `csv:"status"     case:"ignore"`
}

func newPocketAdapter() *pocketFileAdapter {
	return &pocketFileAdapter{
		csvBaseAdapter: csvBaseAdapter[pocketEntry, *pocketBookmarkItem]{
			openFileFn:  pocketOpenFile,
			buildItemFn: newPocketBookmarkItem,
		},
	}
}

func (adapter *pocketFileAdapter) Name(_ context.Context) string {
	return "Pocket"
}

func pocketOpenFile(fo forms.FileOpener) (iter.Seq2[*csv.Reader, error], error) {
	r, err := fo.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close() //nolint:errcheck

	// Try to open the zipfile now
	zr, err := zip.NewReader(r.(io.ReaderAt), fo.Size())
	if err != nil {
		if errors.Is(err, zip.ErrFormat) {
			// The user might have submitted the csv file after
			// manual decompression. Let's fallback to it then.
			return csvOpenFile(fo)
		}

		// This errors is not fatal, hence the simple yield.
		return func(yield func(*csv.Reader, error) bool) {
			yield(nil, err)
		}, nil
	}

	return func(yield func(*csv.Reader, error) bool) {
		// In the absence of any specification, we'll consider that any CSV file contains bookmarks.
		for _, file := range zr.File {
			if !strings.HasSuffix(file.Name, ".csv") {
				continue
			}
			x, err := file.Open()
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(csv.NewReader(x), nil) {
				x.Close() //nolint:errcheck
				return
			}
			x.Close() //nolint:errcheck
		}
	}, nil
}

func newPocketBookmarkItem(e *pocketEntry) (*pocketBookmarkItem, error) {
	uri, err := url.Parse(e.URL)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(allowedSchemes, uri.Scheme) {
		return nil, errSchemeNotAllowed
	}
	uri.Fragment = ""

	res := &pocketBookmarkItem{
		Link:       uri.String(),
		IsArchived: e.Status == "archive",
		Labels:     types.Strings{},
	}

	if title := strings.TrimSpace(e.Title); title != e.URL {
		res.Title = title
	}

	for label := range strings.SplitSeq(e.Tags, "|") {
		if label = strings.TrimSpace(label); label != "" {
			res.Labels = append(res.Labels, label)
		}
	}

	if ts, err := strconv.Atoi(e.TimeAdded); err == nil {
		res.Created = time.Unix(int64(ts), 0).UTC()
	}

	return res, nil
}

func (bi *pocketBookmarkItem) URL() string {
	return bi.Link
}

func (bi *pocketBookmarkItem) Meta() (*BookmarkMeta, error) {
	return &BookmarkMeta{
		Title:      bi.Title,
		Labels:     bi.Labels,
		IsArchived: bi.IsArchived,
		Created:    bi.Created,
	}, nil
}
