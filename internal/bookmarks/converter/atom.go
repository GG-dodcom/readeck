// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package converter

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/bookmarks/dataset"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/atom"
	"codeberg.org/readeck/readeck/pkg/base58"
	"codeberg.org/readeck/readeck/pkg/ctxr"
)

type ctxAtomStylesheetKey struct{}

// WithAtomStylesheet sets an XSL stylesheet URL to the context.
var WithAtomStylesheet, checkAtomStylesheet = ctxr.WithChecker[string](ctxAtomStylesheetKey{})

// AtomExporter is an [Exporter] that produces an Atom feed.
type AtomExporter struct {
	dataset.HTMLConverter
}

// NewAtomExporter return a new [AtomExporter] instance.
func NewAtomExporter() AtomExporter {
	return AtomExporter{
		HTMLConverter: dataset.HTMLConverter{},
	}
}

// Export implements [Exporter].
func (e AtomExporter) Export(ctx context.Context, w io.Writer, r *http.Request, bookmarkList *dataset.BookmarkList) error {
	if w, ok := w.(http.ResponseWriter); ok {
		w.Header().Set("Content-Type", atom.MimeType)
	}

	selfURL := urls.AbsoluteURL(r).String()
	htmlURL := urls.AbsoluteURL(r, "/bookmarks").String()
	if len(r.URL.Query()) > 0 {
		htmlURL += "?" + r.URL.Query().Encode()
	}

	mtimes := []time.Time{configs.BuildTime()}
	for _, b := range bookmarkList.Items {
		mtimes = append(mtimes, b.Updated)
	}
	sort.Slice(mtimes, func(i, j int) bool {
		return mtimes[i].After(mtimes[j])
	})

	feed := &atom.Feed{
		Xmlns:    atom.NS,
		ID:       uuid.NewSHA1(uuid.NameSpaceURL, []byte(selfURL)).URN(),
		Title:    "Readeck's Bookmarks",
		Subtitle: "Readeck's Bookmarks",
		Updated:  atom.Time(mtimes[0]),
		Link: []*atom.Link{
			{
				Href: selfURL,
				Rel:  "self",
				Type: atom.MimeType,
			},
			{
				Href: htmlURL,
				Rel:  "alternate",
				Type: "text/html",
			},
		},
		Icon: urls.AssetURL(r, "img/fi/favicon.ico").String(),
	}

	// Add stylesheet if any
	if s, ok := checkAtomStylesheet(ctx); ok {
		feed.Stylesheet = s
	}

	feed.Entries = make([]*atom.Entry, len(bookmarkList.Items))

	for i, b := range bookmarkList.Items {
		id, _ := base58.DecodeUUID(b.UID)

		feed.Entries[i] = &atom.Entry{
			ID:        id.URN(),
			Title:     b.Title,
			Updated:   atom.Time(b.Updated),
			Published: atom.Time(b.Created),
			Links: []*atom.Link{
				{
					Href: urls.AbsoluteURL(r, "/bookmarks", b.UID).String(),
					Rel:  "alternate",
					Type: "text/html",
				},
			},
		}

		if b.Description != "" {
			feed.Entries[i].Summary = &atom.Summary{
				Type:    "text",
				Content: b.Description,
			}
		}

		resources := bookmarks.BookmarkFiles{}
		for k, v := range b.Files {
			if k == "image" && (b.DocumentType == "photo" || b.DocumentType == "video") {
				resources[k] = &bookmarks.BookmarkFile{
					Name: urls.AbsoluteURL(r, "/bm", b.FilePath, v.Name).String(),
					Type: v.Type,
					Size: v.Size,
				}
			}
		}

		buf := new(bytes.Buffer)
		html, err := e.GetArticle(ctx, b.Bookmark)
		if err != nil {
			return err
		}
		err = atomViews{}.bookmark(b, resources, html).Render(ctx, buf)
		if err != nil {
			return err
		}

		feed.Entries[i].Content = &atom.Content{
			Type:    "html",
			Content: buf.String(),
		}
	}

	return feed.WriteXML(w)
}
