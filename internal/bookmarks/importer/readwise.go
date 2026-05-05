// SPDX-FileCopyrightText: © 2025 Mislav Marohnić <hi@mislav.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package importer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"codeberg.org/readeck/readeck/internal/db/types"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

const (
	// Basically time.RFC3339, but with space character instead of "T".
	readwiseTimeFormat = "2006-01-02 15:04:05-07:00"
)

var (
	_ ImportWorker     = (*readwiseAdapter)(nil)
	_ BookmarkEnhancer = (*readwiseBookmarkItem)(nil)
)

type readwiseAdapter struct {
	csvBaseAdapter[readwiseEntry, *readwiseBookmarkItem]
}

type readwiseEntry struct {
	Title    string `csv:"Title"         case:"ignore"`
	URL      string `csv:"URL"           case:"ignore"`
	Tags     string `csv:"Document tags" case:"ignore"`
	Created  string `csv:"Saved date"    case:"ignore"`
	Location string `csv:"Location"      case:"ignore"`
}

type readwiseBookmarkItem struct {
	Link       string        `json:"url"`
	Title      string        `json:"title"`
	Created    time.Time     `json:"created"`
	Labels     types.Strings `json:"labels"`
	IsArchived bool          `json:"is_archived"`
	IsFavorite bool          `json:"is_favorite"`
}

func newReadwiseAdapter() *readwiseAdapter {
	return &readwiseAdapter{
		csvBaseAdapter: csvBaseAdapter[readwiseEntry, *readwiseBookmarkItem]{
			openFileFn:  csvOpenFile,
			buildItemFn: newReadwiseBookmarkItem,
		},
	}
}

func (adapter *readwiseAdapter) Name(ctx context.Context) string {
	return forms.GetTranslator(ctx).Gettext("Readwise Reader CSV")
}

func newReadwiseBookmarkItem(e *readwiseEntry) (*readwiseBookmarkItem, error) {
	res := &readwiseBookmarkItem{}
	uri, err := url.Parse(e.URL)
	if err != nil {
		return res, err
	}

	if !slices.Contains(allowedSchemes, uri.Scheme) {
		return res, errSchemeNotAllowed
	}
	uri.Fragment = ""
	res.Link = uri.String()

	res.Title = strings.TrimSpace(e.Title)
	if e.Created != "" {
		res.Created, err = time.Parse(readwiseTimeFormat, e.Created)
		if err != nil {
			return res, fmt.Errorf("error parsing created timestamp: %w", err)
		}
	}

	if e.Tags != "" {
		tags, err := parseReadwiseTags(e.Tags)
		if err != nil {
			return res, fmt.Errorf("error parsing tags: %w", err)
		}
		if slices.Contains(tags, "favorite") {
			res.IsFavorite = true
			res.Labels = slices.DeleteFunc(tags, func(tag string) bool {
				return tag == "favorite"
			})
		} else {
			res.Labels = tags
		}
	}

	if strings.ToLower(e.Location) == "archive" {
		res.IsArchived = true
	}

	return res, nil
}

func (bi *readwiseBookmarkItem) URL() string {
	return bi.Link
}

func (bi *readwiseBookmarkItem) Meta() (*BookmarkMeta, error) {
	return &BookmarkMeta{
		Title:      bi.Title,
		Created:    bi.Created,
		Labels:     bi.Labels,
		IsArchived: bi.IsArchived,
		IsMarked:   bi.IsFavorite,
	}, nil
}

// Readwise Reader CSV export encodes document tags as a JSON-like array, but it's not valid JSON
// due to single quotes used. Since Readwise does not allow double quotes nor backslashes in tag
// values, we can get away with a straightforward parser.
func parseReadwiseTags(field string) ([]string, error) {
	var tags []string

	r := bufio.NewReader(strings.NewReader(field))
	if delim, err := r.ReadByte(); err != nil {
		return tags, err
	} else if delim != '[' {
		return tags, errors.New("invalid label format")
	}

	for {
		char, err := r.ReadByte()
		if err != nil {
			return tags, err
		}

		if char == ']' {
			break
		}

		if char == '\'' || char == '"' {
			tagValue, err := r.ReadString(char)
			if err != nil {
				return tags, err
			}
			tags = append(tags, tagValue[:len(tagValue)-1])
		}
	}

	return tags, nil
}
