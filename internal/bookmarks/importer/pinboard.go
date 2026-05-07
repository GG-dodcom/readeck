// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package importer

import (
	"context"
	"strings"

	"github.com/araddon/dateparse"

	"codeberg.org/readeck/readeck/internal/db/types"
	"codeberg.org/readeck/readeck/pkg/forms"
)

var (
	_ ImportWorker     = (*pinboardAdapter)(nil)
	_ BookmarkEnhancer = (*pinboardItem)(nil)
)

type pinboardAdapter struct {
	jsonBaseAdapter[[]*pinboardItem, *pinboardItem]
}

type pinboardItem struct {
	Link        string `json:"href"`
	Description string `json:"description"`
	Time        string `json:"time"`
	Tags        string `json:"tags"`
	ToRead      string `json:"toread"`
	Extended    string `json:"extended"`
}

func newPinboardAdapter() *pinboardAdapter {
	return &pinboardAdapter{
		jsonBaseAdapter: jsonBaseAdapter[[]*pinboardItem, *pinboardItem]{
			loadItems: loadPinboardItems,
		},
	}
}

func (adapter *pinboardAdapter) Name(ctx context.Context) string {
	return forms.GetTranslator(ctx).Gettext("Pinboard JSON backup File")
}

func (bi *pinboardItem) URL() string {
	return bi.Link
}

func (bi *pinboardItem) Meta() (*BookmarkMeta, error) {
	item := &BookmarkMeta{
		Title:       bi.Description,
		Description: bi.Extended,
		Labels:      types.Strings{},
		IsArchived:  bi.ToRead != "yes",
	}

	if l := strings.TrimSpace(bi.Tags); len(l) > 0 {
		item.Labels = strings.Split(l, " ")
	}

	if d, err := dateparse.ParseAny(bi.Time); err == nil {
		item.Created = d.UTC()
	}

	return item, nil
}

func loadPinboardItems(gi *[]*pinboardItem) ([]*pinboardItem, error) {
	// Pinboard exports a list of items, so it's very easy to convert directly
	// from and to the same [pinboardItem] list.
	return *gi, nil
}
