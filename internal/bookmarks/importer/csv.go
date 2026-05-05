// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package importer

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/araddon/dateparse"

	"codeberg.org/readeck/readeck/internal/db/types"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

var (
	_ ImportWorker     = (*csvAdapter)(nil)
	_ BookmarkEnhancer = (*csvBookmarkItem)(nil)
)

type csvAdapter struct {
	csvBaseAdapter[csvEntry, *csvBookmarkItem]
}

type csvBookmarkItem struct {
	Link       string        `json:"url"`
	Title      string        `json:"title"`
	Created    time.Time     `json:"created"`
	Labels     types.Strings `json:"labels"`
	IsArchived bool          `json:"is_archived"`
}

type csvEntry struct {
	URL     string `csv:"url"               case:"ignore"`
	Title   string `csv:"title"             case:"ignore"`
	Folder  string `csv:"folder,state"      case:"ignore"`
	Created string `csv:"created,timestamp" case:"ignore"`
	Labels  string `csv:"labels,tags"       case:"ignore"`
}

func newCsvAdapter() *csvAdapter {
	return &csvAdapter{
		csvBaseAdapter: csvBaseAdapter[csvEntry, *csvBookmarkItem]{
			openFileFn:  csvOpenFile,
			buildItemFn: newCsvBookmarkItem,
		},
	}
}

func (adapter *csvAdapter) Name(ctx context.Context) string {
	return forms.GetTranslator(ctx).Gettext("CSV File")
}

func newCsvBookmarkItem(e *csvEntry) (*csvBookmarkItem, error) {
	res := &csvBookmarkItem{}
	uri, err := url.Parse(e.URL)
	if err != nil {
		return res, err
	}
	if !slices.Contains(allowedSchemes, uri.Scheme) {
		return res, errSchemeNotAllowed
	}
	uri.Fragment = ""

	res.Link = uri.String()
	res.Title = e.Title
	if ts, err := strconv.Atoi(e.Created); err == nil {
		res.Created = time.Unix(int64(ts), 0).UTC()
	} else {
		res.Created, _ = dateparse.ParseAny(e.Created)
	}
	if e.Labels != "" && e.Labels != "[]" {
		_ = json.Unmarshal([]byte(e.Labels), &res.Labels)
	}
	if e.Folder == "archive" {
		res.IsArchived = true
	}

	return res, nil
}

func (bi *csvBookmarkItem) URL() string {
	return bi.Link
}

func (bi *csvBookmarkItem) Meta() (*BookmarkMeta, error) {
	return &BookmarkMeta{
		Title:      bi.Title,
		Created:    bi.Created,
		Labels:     bi.Labels,
		IsArchived: bi.IsArchived,
	}, nil
}
