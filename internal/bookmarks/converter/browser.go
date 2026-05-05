// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package converter

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"codeberg.org/readeck/readeck/internal/bookmarks/dataset"
)

// BrowserExporter is an [IterExporter] that produces a browser bookmark file.
type BrowserExporter struct{}

type browserExportSection struct {
	name  string
	items []*dataset.Bookmark
}

// IterExport implements [IterExporter].
func (e BrowserExporter) IterExport(ctx context.Context, w io.Writer, _ *http.Request, bookmarkSeq *dataset.BookmarkIterator) error {
	if w, ok := w.(http.ResponseWriter); ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(
			`attachment; filename="readeck-bookmarks-%s.html"`,
			time.Now().UTC().Format(time.DateOnly),
		))
	}

	uncategorized := []*dataset.Bookmark{}
	favorite := []*dataset.Bookmark{}
	archived := []*dataset.Bookmark{}

	for b, err := range bookmarkSeq.Items {
		if err != nil {
			return err
		}
		switch {
		case b.IsMarked:
			favorite = append(favorite, b)
		case b.IsArchived:
			archived = append(archived, b)
		default:
			uncategorized = append(uncategorized, b)
		}
	}

	return browserViews{}.export([]browserExportSection{
		{"Favorite", favorite},
		{"Archived", archived},
		{"Uncategorized", uncategorized},
	}).Render(ctx, w)
}
