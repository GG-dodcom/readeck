// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package importer

import (
	"context"
	"time"

	"golang.org/x/net/html"

	"github.com/araddon/dateparse"

	"codeberg.org/readeck/readeck/internal/db/types"
)

var (
	_ ImportWorker     = (*linkwardenAdapter)(nil)
	_ BookmarkEnhancer = (*linkwardenItem)(nil)
)

type linkwardenAdapter struct {
	jsonBaseAdapter[linkwardenFile, *linkwardenItem]
}

type linkwardenFile struct {
	Collections []struct {
		ID    int    `json:"id"`
		Name  string `json:"name"`
		Links []struct {
			ID      int    `json:"id"`
			URL     string `json:"url"`
			Name    string `json:"name"`
			Type    string `json:"type"`
			Created string `json:"createdAt"`
			Tags    []struct {
				Name string `json:"name"`
			} `json:"tags"`
		}
	} `json:"collections"`
	Pinned []struct {
		ID int `json:"id"`
	} `json:"pinnedLinks"`
}

type linkwardenItem struct {
	Link     string        `json:"url"`
	Title    string        `json:"title"`
	Labels   types.Strings `json:"labels"`
	Created  time.Time     `json:"created"`
	IsMarked bool          `json:"is_marked"`
}

func newLinkwardenAdapter() *linkwardenAdapter {
	return &linkwardenAdapter{
		jsonBaseAdapter: jsonBaseAdapter[linkwardenFile, *linkwardenItem]{
			loadItems: loadLinkwardenItems,
		},
	}
}

func (adapter *linkwardenAdapter) Name(_ context.Context) string {
	return "Linkwarden"
}

func (bi *linkwardenItem) URL() string {
	return bi.Link
}

func (bi *linkwardenItem) Meta() (*BookmarkMeta, error) {
	return &BookmarkMeta{
		Title:    bi.Title,
		Created:  bi.Created,
		IsMarked: bi.IsMarked,
		Labels:   bi.Labels,
	}, nil
}

func loadLinkwardenItems(lf *linkwardenFile) ([]*linkwardenItem, error) {
	idMap := map[int]int{}
	items := []*linkwardenItem{}

	for _, c := range lf.Collections {
		for _, l := range c.Links {
			if l.Type != "url" && l.Type != "image" {
				continue
			}

			if _, ok := idMap[l.ID]; ok {
				continue
			}

			item := &linkwardenItem{
				Link:    l.URL,
				Title:   html.UnescapeString(l.Name),
				Created: time.Now().UTC(),
			}

			if d, err := dateparse.ParseAny(l.Created); err == nil {
				item.Created = d.UTC()
			}

			for _, t := range l.Tags {
				item.Labels = append(item.Labels, t.Name)
			}

			items = append(items, item)
			idMap[l.ID] = len(items) - 1
		}
	}

	// Set favorites
	for _, l := range lf.Pinned {
		if i, ok := idMap[l.ID]; ok {
			items[i].IsMarked = true
		}
	}

	return items, nil
}
