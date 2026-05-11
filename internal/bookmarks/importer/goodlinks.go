// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package importer

import (
	"context"
	"time"

	"codeberg.org/readeck/readeck/internal/db/types"
	"codeberg.org/readeck/readeck/pkg/forms"
)

var (
	_ ImportWorker     = (*goodlinksAdapter)(nil)
	_ BookmarkEnhancer = (*goodlinksItem)(nil)
)

type goodlinksAdapter struct {
	jsonBaseAdapter[[]*goodlinksItem, *goodlinksItem]
}

type goodlinksItem struct {
	Link    string        `json:"url"`
	Title   string        `json:"title"`
	AddedAt float64       `json:"addedAt"`
	Tags    types.Strings `json:"tags"`
	Starred bool          `json:"starred"`
}

func newGoodlinksAdapter() *goodlinksAdapter {
	return &goodlinksAdapter{
		jsonBaseAdapter: jsonBaseAdapter[[]*goodlinksItem, *goodlinksItem]{
			loadItems: loadGoodlinksItems,
		},
	}
}

func (adapter *goodlinksAdapter) Name(ctx context.Context) string {
	return forms.GetTranslator(ctx).Gettext("GoodLinks Export File")
}

func (bi *goodlinksItem) URL() string {
	return bi.Link
}

func (bi *goodlinksItem) Meta() (*BookmarkMeta, error) {
	return &BookmarkMeta{
		Title:    bi.Title,
		Created:  time.Unix(int64(bi.AddedAt), 0).UTC(),
		Labels:   bi.Tags,
		IsMarked: bi.Starred,
	}, nil
}

func loadGoodlinksItems(gi *[]*goodlinksItem) ([]*goodlinksItem, error) {
	// Goodlinks exports a list of items, so it's very easy to convert directly
	// from and to the same [goodlinksItem] list.
	return *gi, nil
}
