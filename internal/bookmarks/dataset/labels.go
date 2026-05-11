// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package dataset

import (
	"context"
	"net/url"
	"strconv"

	"github.com/doug-martin/goqu/v9"

	"codeberg.org/readeck/readeck/internal/db/scanner"
	"codeberg.org/readeck/readeck/internal/server/urls"
)

// LabelList is a list of [*Label].
type LabelList []*Label

// Label contains a label's information.
type Label struct {
	Name          string `json:"name"           db:"name"`
	Count         int    `json:"count"          db:"count"`
	Href          string `json:"href"           db:"-"`
	HrefBookmarks string `json:"href_bookmarks" db:"-"`
}

// NewLabelList returns a new [LabelList] from a select dataset.
func NewLabelList(ctx context.Context, ds *goqu.SelectDataset) (LabelList, error) {
	res := LabelList{}

	for item, err := range scanner.IterTransform(ctx, ds, NewLabel) {
		if err != nil {
			return nil, err
		}
		res = append(res, item)
	}

	return res, nil
}

// NewLabel returns a new [*Label], setting the necessary URLs.
func NewLabel(ctx context.Context, l *Label) *Label {
	u := urls.AbsoluteURLContext(ctx, "/api/bookmarks/labels")
	u.RawQuery = url.Values{"name": []string{string(l.Name)}}.Encode()

	l.Href = u.String()
	l.HrefBookmarks = urls.AbsoluteURLContext(ctx, "/api/bookmarks").String() + "?" + url.Values{
		"labels": []string{strconv.Quote(l.Name)},
	}.Encode()
	return l
}
