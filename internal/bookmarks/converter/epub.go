// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package converter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-shiori/dom"
	"github.com/google/uuid"
	"golang.org/x/net/html"

	"codeberg.org/readeck/readeck/assets"
	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/bookmarks/dataset"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/ctxr"
	"codeberg.org/readeck/readeck/pkg/epub"
	"codeberg.org/readeck/readeck/pkg/utils"
)

type ctxEnableEPUBNotesKey struct{}

// Context helpers.
var (
	WithEnableEPUBNotes, getEnableEPUBNotes = ctxr.WithChecker[bool](ctxEnableEPUBNotesKey{})
)

// EPUBExporter is a content exporter that produces EPUB files.
type EPUBExporter struct {
	dataset.HTMLConverter
	Collection *bookmarks.Collection
}

// NewEPUBExporter returns a new [EPUBExporter] instance.
func NewEPUBExporter() EPUBExporter {
	return EPUBExporter{
		HTMLConverter: dataset.HTMLConverter{},
	}
}

// IterExport implements [IterExporter].
// It writes an EPUB file on the provided [io.Writer].
func (e EPUBExporter) IterExport(ctx context.Context, w io.Writer, r *http.Request, bookmarkSeq *dataset.BookmarkIterator) error {
	// Define a title, date, siteName and filename
	title := "Readeck Bookmarks"
	date := time.Now().UTC()
	siteName := "Readeck"
	if e.Collection != nil {
		title = e.Collection.Name
		siteName = "Readeck Collection"
	}

	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(urls.AbsoluteURL(r, "").String()))
	var m *epubMaker
	var err error

	defer func() {
		if m == nil {
			return
		}
		if err == nil {
			m.SetTitle(title)
			m.SetCreator(siteName)
			err = m.WritePackage()
		}
		m.Close() //nolint:errcheck
	}()

	ctx = dataset.WithURLReplacer(ctx, func(_ *bookmarks.Bookmark) func(name string) string {
		return func(name string) string {
			return "./Images/" + path.Base(name)
		}
	})

	count, err := bookmarkSeq.Count()
	if err != nil {
		return err
	}
	i := 0
	for b, err := range bookmarkSeq.Items {
		if err != nil {
			return err
		}

		// Only one bookmark? Set a title
		if e.Collection == nil && count == 1 {
			title = b.Title
			date = b.Created
			siteName = b.SiteName
		}

		// Send header before first item
		if w, ok := w.(http.ResponseWriter); ok && i == 0 {
			w.Header().Set("Content-Type", "application/epub+zip")
			w.Header().Set("Content-Disposition", fmt.Sprintf(
				`attachment; filename="%s-%s.epub"`,
				utils.Slug(strings.TrimSuffix(utils.ShortText(title, 40), "...")),
				date.Format(time.DateOnly),
			))
		}

		// Only now can we create the epubMaker.
		if i == 0 {
			m, err = newEpubMaker(w, id)
			if err != nil {
				return err
			}
		}

		if err = m.addBookmark(ctx, r, e, b); err != nil {
			return err
		}

		i++
	}

	return nil
}

// epubMaker is a wrapper around epub.Writer with extra methods to
// create an epub file from one or many bookmarks.
type epubMaker struct {
	*epub.Writer
}

// newEpubMaker creates a new EpubMaker instance.
func newEpubMaker(w io.Writer, id uuid.UUID) (*epubMaker, error) {
	m := &epubMaker{epub.New(w)}
	if err := m.Bootstrap(); err != nil {
		return nil, err
	}

	m.SetID(id.URN())
	m.SetTitle("Readeck ebook")
	m.SetLanguage("en")

	if err := m.addStylesheet(); err != nil {
		return nil, err
	}
	return m, nil
}

// addStylesheet adds the stylesheet to the epub file.
func (m *epubMaker) addStylesheet() error {
	f, err := assets.StaticFilesFS().Open("epub.css")
	if err != nil {
		return err
	}
	defer f.Close()
	return m.AddFile("stylesheet", "styles/stylesheet.css", "text/css", f)
}

// addBookmark adds a bookmark, with all its resources, to the epub file.
func (m *epubMaker) addBookmark(ctx context.Context, _ *http.Request, e EPUBExporter, b *dataset.Bookmark) (err error) {
	var c *bookmarks.BookmarkContainer
	if c, err = b.OpenContainer(); err != nil {
		return
	}
	defer c.Close()

	// Add all the resource files to the book. They are only images for now.
	for _, x := range c.ListResources() {
		err = func() error {
			fp, err := x.Open()
			if err != nil {
				return err
			}
			defer fp.Close() //nolint:errcheck
			return m.AddImage(
				"res-"+strings.TrimSuffix(path.Base(x.Name), path.Ext(x.Name)),
				path.Join("Images", path.Base(x.Name)),
				fp,
			)
		}()
		if err != nil {
			return
		}
	}

	// Build the other resource list
	resources := bookmarks.BookmarkFiles{}
	for k, v := range b.Files {
		if k == "icon" || k == "image" && (b.DocumentType == "photo" || b.DocumentType == "video") {
			resources[k] = &bookmarks.BookmarkFile{
				Name: path.Join("Images", fmt.Sprintf("%s-%s%s", k, b.UID, path.Ext(v.Name))),
				Type: v.Type,
				Size: v.Size,
			}
		}
	}

	// Add all fixed resources (image, icon) to the container
	for k, v := range resources {
		err = func() error {
			fp, err := c.Open(b.Files[k].Name) // original path
			if err != nil {
				return err
			}
			defer fp.Close() //nolint:errcheck

			return m.AddImage(
				fmt.Sprintf("%s-%s", k, b.UID),
				v.Name,
				fp,
			)
		}()
		if err != nil {
			return
		}
	}

	// Set highlights and notes
	withNotes, _ := getEnableEPUBNotes(ctx)
	notes := []string{}

	ctx = dataset.WithAnnotationTag(ctx, "mark", func(a *bookmarks.BookmarkAnnotation, n *html.Node, index, ln int) {
		if withNotes && a.Note != "" && index+1 == ln {
			notes = append(notes, a.Note)
			link := dom.CreateElement("a")
			link.Attr = []html.Attribute{
				{Key: "id", Val: "noteref" + strconv.Itoa(len(notes))},
				{Key: "href", Val: "#note" + strconv.Itoa(len(notes))},
				{Namespace: "epub", Key: "type", Val: "noteref"},
			}
			dom.AppendChild(link, dom.CreateTextNode(strconv.Itoa(len(notes))))
			n.Parent.InsertBefore(link, n.NextSibling)
		}
	})

	buf := new(bytes.Buffer)
	html, err := e.GetArticle(ctx, b.Bookmark)
	if err != nil {
		return err
	}
	err = epubViews{}.bookmark(b, resources, notes, html).Render(ctx, buf)
	if err != nil {
		return err
	}

	return m.AddChapter(
		"page-"+b.UID,
		b.Title,
		b.UID+".html",
		buf,
	)
}
