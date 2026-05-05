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
	"strings"
	"time"

	"github.com/wneessen/go-mail"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/bookmarks/dataset"
	"codeberg.org/readeck/readeck/internal/email"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/base58"
	"codeberg.org/readeck/readeck/pkg/utils"
)

// HTMLEmailExporter is a content exporter that send bookmarks by emails.
type HTMLEmailExporter struct {
	dataset.HTMLConverter
	to        string
	cidPrefix string
	options   []email.MessageOption
}

// NewHTMLEmailExporter returns a new [HTMLEmailExporter] instance.
func NewHTMLEmailExporter(to string, options ...email.MessageOption) HTMLEmailExporter {
	return HTMLEmailExporter{
		HTMLConverter: dataset.HTMLConverter{},
		to:            to,
		cidPrefix:     base58.NewUUID(),
		options:       options,
	}
}

// Export implements [Exporter].
// It create an email with a text/plain and text/html version and attaches images
// as inline resources.
func (e HTMLEmailExporter) Export(ctx context.Context, _ io.Writer, r *http.Request, bookmarkList *dataset.BookmarkList) error {
	if l := len(bookmarkList.Items); l != 1 {
		return fmt.Errorf("HTMLEmailExporter can only export one bookmark. Got %d", l)
	}
	b := bookmarkList.Items[0]

	ctx = email.WithSiteURL(ctx, urls.AbsoluteURL(r, "/"))
	component, err := newEmailComponent(ctx, e, b)
	if err != nil {
		return err
	}

	// Prepare message
	msg, err := email.NewMessage(
		ctx,
		configs.Config.Email.FromNoReply.String(),
		e.to,
		"[Readeck] "+utils.ShortText(b.Title, 80),
		append(e.options, email.WithComponent(component))...,
	)
	if err != nil {
		return err
	}

	// Attach resources
	var c *bookmarks.BookmarkContainer
	if c, err = b.OpenContainer(); err != nil {
		return err
	}
	defer c.Close()

	for _, x := range c.ListResources() {
		if err = func() error {
			fp, err := x.Open()
			if err != nil {
				return err
			}
			defer fp.Close() // nolint:errcheck
			name := path.Base(x.Name)
			return msg.EmbedReader(name, fp,
				mail.WithFileContentID(e.cidPrefix+"."+name),
			)
		}(); err != nil {
			return err
		}
	}

	if b.DocumentType == "photo" || b.DocumentType == "video" {
		if i, ok := b.Files["image"]; ok {
			fp, err := c.Open(i.Name)
			if err != nil {
				return err
			}
			defer fp.Close() // nolint:errcheck

			name := path.Base(i.Name)
			if err = msg.EmbedReader(name, fp,
				mail.WithFileContentID(e.cidPrefix+"."+name),
			); err != nil {
				return err
			}
		}
	}

	return email.Send(msg)
}

type emailComponent struct {
	html  io.Reader
	item  *dataset.Bookmark
	image *bookmarks.BookmarkFile
}

func newEmailComponent(ctx context.Context, e HTMLEmailExporter, b *dataset.Bookmark) (*emailComponent, error) {
	ctx = dataset.WithURLReplacer(ctx, func(_ *bookmarks.Bookmark) func(name string) string {
		return func(name string) string {
			return "cid:" + e.cidPrefix + "." + path.Base(name)
		}
	})
	ctx = dataset.WithAnnotationTag(ctx, "mark", nil)
	html, err := e.GetArticle(ctx, b.Bookmark)
	if err != nil {
		return nil, err
	}

	image := &bookmarks.BookmarkFile{}
	if i, ok := b.Files["image"]; ok {
		*image = *i
		image.Name = "cid:" + e.cidPrefix + "." + path.Base(image.Name)
	}

	return &emailComponent{
		html:  html,
		item:  b,
		image: image,
	}, nil
}

func (c *emailComponent) Render(ctx context.Context, w io.Writer) error {
	return c.renderer().Render(ctx, w)
}

// EPUBEmailExporter is a content exporter that send converted bookmarks as EPUB attachment
// by emails.
type EPUBEmailExporter struct {
	to      string
	options []email.MessageOption
}

// NewEPUBEmailExporter returns an [NewEPUBEmailExporter] instance.
func NewEPUBEmailExporter(to string, options ...email.MessageOption) EPUBEmailExporter {
	return EPUBEmailExporter{
		to:      to,
		options: options,
	}
}

// Export implements [Exporter].
// It create an email with the bookmark's EPUB file attached to it.
func (e EPUBEmailExporter) Export(ctx context.Context, _ io.Writer, r *http.Request, bookmarkList *dataset.BookmarkList) error {
	if l := len(bookmarkList.Items); l != 1 {
		return fmt.Errorf("EPUBEmailExporter can only export one bookmark. Got %d", l)
	}
	b := bookmarkList.Items[0]

	ctx = email.WithSiteURL(ctx, urls.AbsoluteURL(r, "/"))

	msg, err := email.NewMessage(
		ctx,
		configs.Config.Email.FromNoReply.String(),
		e.to,
		"[Readeck EPUB] "+utils.ShortText(b.Title, 80),
		append(e.options, email.WithMDText(e.msg(ctx, b.Title)))...,
	)
	if err != nil {
		return err
	}

	w := new(bytes.Buffer)
	ee := NewEPUBExporter()
	if err := ee.IterExport(ctx, w, r, bookmarkList.ToIterator()); err != nil {
		return err
	}
	if err := msg.AttachReader(fmt.Sprintf(
		"%s-%s.epub",
		b.Created.Format(time.DateOnly),
		utils.Slug(strings.TrimSuffix(utils.ShortText(b.Title, 40), "...")),
	), w); err != nil {
		return err
	}

	return email.Send(msg)
}

func (EPUBEmailExporter) msg(ctx context.Context, title string) string {
	return server.LocaleContext(ctx).Gettext(`
Hi,

Here is your ebook for: **%s**
`, title)
}
