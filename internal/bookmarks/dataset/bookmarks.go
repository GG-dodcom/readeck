// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package dataset

import (
	"context"
	"fmt"
	"hash"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/go-shiori/dom"
	"golang.org/x/net/html"

	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/bookmarks/tasks"
	"codeberg.org/readeck/readeck/internal/db/scanner"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
	"codeberg.org/readeck/readeck/pkg/utils"
)

// BookmarkList is a collection of [Bookmark] items.
type BookmarkList struct {
	Count      int64
	Pagination server.Pagination
	Items      []*Bookmark
}

// BookmarkIterator is an iterator over [Bookmark] results.
type BookmarkIterator struct {
	counted bool
	count   int64
	Items   scanner.Iterator[Bookmark]
	ds      *goqu.SelectDataset
}

// NewBookmarkIterator returns a [BookmarkIterator] instance.
func NewBookmarkIterator(ctx context.Context, ds *goqu.SelectDataset) *BookmarkIterator {
	return &BookmarkIterator{
		Items: scanner.IterTransform(ctx, ds, NewBookmark),
		ds:    ds,
	}
}

// Count returns the number of elements contained in the dataset.
func (bi *BookmarkIterator) Count() (int64, error) {
	var err error
	if !bi.counted {
		bi.count, err = bi.ds.ClearOrder().ClearLimit().ClearOffset().Count()
		bi.counted = true
	}

	return bi.count, err
}

// UpdateEtag implements [server.Etagger] interface.
func (bi BookmarkIterator) UpdateEtag(h hash.Hash) {
	if bi.ds == nil {
		return
	}

	rs, err := bi.ds.Select("b.uid", "b.updated").Executor().Query()
	if err != nil {
		return
	}
	defer rs.Close() //nolint:errcheck

	for rs.Next() {
		var i string
		var u time.Time
		if err = rs.Scan(&i, &u); err != nil {
			continue
		}
		io.WriteString(h, i+strconv.FormatInt(u.UTC().Unix(), 10))
	}
}

// NewBookmarkList returns a new [*BookmarkList].
//
//nolint:dupl
func NewBookmarkList(ctx context.Context, ds *goqu.SelectDataset) (*BookmarkList, error) {
	res := &BookmarkList{
		Items: []*Bookmark{},
	}

	var err error
	if res.Count, err = ds.ClearOrder().ClearLimit().ClearOffset().Count(); err != nil {
		return nil, err
	}

	if limit, ok := ds.GetClauses().Limit().(uint); ok {
		res.Pagination = server.NewPagination(ctx,
			int(res.Count), int(limit), int(ds.GetClauses().Offset()),
		)
	}

	if res.Count == 0 {
		return res, nil
	}

	for item, err := range scanner.IterTransform(ctx, ds, NewBookmark) {
		if err != nil {
			return nil, err
		}
		res.Items = append(res.Items, item)
	}

	return res, nil
}

// UpdateEtag implements [server.Etagger].
func (bl BookmarkList) UpdateEtag(h hash.Hash) {
	for _, item := range bl.Items {
		item.UpdateEtag(h)
	}
}

// ToIterator convert the list to a [BookmarkIterator].
// Since the result is not bound to a request, its UpdateEtag method
// won't do anything.
func (bl BookmarkList) ToIterator() *BookmarkIterator {
	return &BookmarkIterator{
		counted: true,
		count:   int64(len(bl.Items)),
		Items: func(yield func(*Bookmark, error) bool) {
			for _, b := range bl.Items {
				if !yield(b, nil) {
					return
				}
			}
		},
	}
}

// Bookmark is a serialized bookmark instance that can
// be used directly on the API or by an HTML template.
type Bookmark struct {
	*bookmarks.Bookmark `json:"-"`

	ID              string                        `json:"id"`
	Href            string                        `json:"href"`
	Created         time.Time                     `json:"created"`
	Updated         time.Time                     `json:"updated"`
	State           bookmarks.BookmarkState       `json:"state"`
	Loaded          bool                          `json:"loaded"`
	URL             string                        `json:"url"`
	Title           string                        `json:"title"`
	SiteName        string                        `json:"site_name"`
	Site            string                        `json:"site"`
	Published       *time.Time                    `json:"published,omitempty"`
	Authors         []string                      `json:"authors"`
	Lang            string                        `json:"lang"`
	TextDirection   string                        `json:"text_direction"`
	DocumentType    string                        `json:"document_type"`
	Type            string                        `json:"type"`
	HasArticle      bool                          `json:"has_article"`
	Description     string                        `json:"description"`
	OmitDescription *bool                         `json:"omit_description,omitempty"`
	IsDeleted       bool                          `json:"is_deleted"`
	IsMarked        bool                          `json:"is_marked"`
	IsArchived      bool                          `json:"is_archived"`
	Labels          []string                      `json:"labels"`
	ReadProgress    int                           `json:"read_progress"`
	ReadAnchor      string                        `json:"read_anchor,omitempty"`
	Annotations     bookmarks.BookmarkAnnotations `json:"-"`
	Resources       map[string]*BookmarkFile      `json:"resources"`
	Embed           string                        `json:"embed,omitempty"`
	EmbedHostname   string                        `json:"embed_domain,omitempty"`
	Errors          []string                      `json:"errors,omitempty"`
	Links           bookmarks.BookmarkLinks       `json:"links,omitempty"`
	WordCount       int                           `json:"word_count,omitempty"`
	ReadingTime     int                           `json:"reading_time,omitempty"`

	context        context.Context
	mediaURL       *url.URL
	videoPlayerURL *url.URL
}

// BookmarkFile is a file attached to a bookmark. If the file is
// an image, the "Width" and "Height" values will be filled.
type BookmarkFile struct {
	Src    string `json:"src"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// NewBookmark builds a [Bookmark] from a [bookmarks.Bookmark] instance.
func NewBookmark(ctx context.Context, b *bookmarks.Bookmark) *Bookmark {
	bookmarkURL := urls.AbsoluteURLContext(ctx, "/api/bookmarks", b.UID)

	res := &Bookmark{
		Bookmark:      b,
		ID:            b.UID,
		Href:          bookmarkURL.String(),
		Created:       b.Created.UTC(),
		Updated:       b.Updated.UTC(),
		State:         b.State,
		Loaded:        b.State != bookmarks.StateLoading,
		URL:           b.URL,
		Title:         b.Title,
		SiteName:      b.SiteName,
		Site:          b.Site,
		Published:     b.Published,
		Authors:       b.Authors,
		Lang:          b.Lang,
		TextDirection: b.TextDirection,
		DocumentType:  b.DocumentType,
		Description:   b.Description,
		IsDeleted:     tasks.DeleteBookmarkTask.IsRunning(b.ID),
		IsMarked:      b.IsMarked,
		IsArchived:    b.IsArchived,
		ReadProgress:  b.ReadProgress,
		ReadAnchor:    b.ReadAnchor,
		WordCount:     b.WordCount,
		ReadingTime:   b.ReadingTime(),
		Labels:        make([]string, 0),
		Annotations:   b.Annotations,
		Resources:     make(map[string]*BookmarkFile),
		Links:         b.Links,
	}

	res.context = ctx
	res.mediaURL = urls.AbsoluteURLContext(ctx, "/bm", b.FilePath)
	res.videoPlayerURL = urls.AbsoluteURLContext(ctx, "/videoplayer")

	if b.Labels != nil {
		res.Labels = b.Labels
	}

	switch res.DocumentType {
	case "video":
		res.Type = "video"
	case "image", "photo":
		res.Type = "photo"
	default:
		res.Type = "article"
	}

	// Check if description is somewhere at the beginning of the content.
	// Only when we have a text content (full bookmark info)
	if b.Text != "" && b.Description != "" {
		omitDescription := strings.Contains(
			utils.ToLowerTextOnly(b.Text[:min(len(b.Text), int(len(b.Description)*3))]),
			utils.ToLowerTextOnly(b.Description),
		)
		res.OmitDescription = &omitDescription
	}

	// Files and resources
	for k, v := range b.Files {
		if path.Dir(v.Name) != "img" {
			continue
		}

		f := &BookmarkFile{
			Src: res.mediaURL.JoinPath("/", v.Name).String(),
		}

		if v.Size != [2]int{0, 0} {
			f.Width = v.Size[0]
			f.Height = v.Size[1]
		}
		res.Resources[k] = f
	}

	if v, ok := b.Files["props"]; ok {
		res.Resources["props"] = &BookmarkFile{Src: bookmarkURL.JoinPath("x", v.Name).String()}
	}
	if v, ok := b.Files["log"]; ok {
		res.Resources["log"] = &BookmarkFile{Src: bookmarkURL.JoinPath("x", v.Name).String()}
	}
	if _, ok := b.Files["article"]; ok {
		res.HasArticle = true
		res.Resources["article"] = &BookmarkFile{Src: bookmarkURL.JoinPath("article").String()}
	}

	return res
}

// MediaURL returns the absolute URL for an article media resource.
func (bi Bookmark) MediaURL(name string) string {
	// return bi.mediaURL
	return bi.mediaURL.JoinPath(name).String()
}

// GetArticle calls [HTMLConverter.GetArticle]
// with URL replacer and annotation tag properly setup.
func (bi Bookmark) GetArticle() (*strings.Reader, error) {
	ctx := bi.context

	// Set resource URL replacer, for images
	ctx = WithURLReplacer(ctx, func(_ *bookmarks.Bookmark) func(name string) string {
		return bi.MediaURL
	})

	// Set default annotation tag and callback
	if tag, _ := getAnnotationTag(ctx); tag == "" {
		ctx = WithAnnotationTag(ctx, AnnotationTag, AnnotationCallback(true))
	}

	// Get article from converter
	return HTMLConverter{}.GetArticle(
		ctx,
		bi.Bookmark,
	)
}

// SetEmbed sets the Embed and EmbedHostname item properties.
// The original embed value must be an iframe. We extract the "src"
// URL and store its hostname that we can later use in the CSP policy.
// A special case for youtube for which we force
// the use of youtube-nocookie.com.
func (bi *Bookmark) SetEmbed() error {
	if bi.Bookmark.Embed == "" || bi.EmbedHostname != "" {
		return nil
	}
	node, err := html.Parse(strings.NewReader(bi.Bookmark.Embed))
	if err != nil {
		return err
	}
	embed := dom.QuerySelector(node, "iframe,hls,video")
	if embed == nil {
		return nil
	}

	src, err := url.Parse(dom.GetAttribute(embed, "src"))
	if err != nil {
		return err
	}

	// Force youtube iframes to use the "nocookie" variant.
	if src.Host == "www.youtube.com" {
		src.Host = "www.youtube-nocookie.com"
	}

	playerURL := &url.URL{}

	switch dom.TagName(embed) {
	case "iframe":
		// Set the embed block and its hostname
		dom.SetAttribute(embed, "src", src.String())
		dom.SetAttribute(embed, "credentialless", "true")
		dom.SetAttribute(embed, "allowfullscreen", "true")
		dom.SetAttribute(embed, "referrerpolicy", "strict-origin-when-cross-origin")
		dom.SetAttribute(embed, "sandbox", "allow-scripts allow-same-origin")
		dom.SetAttribute(embed, "allow", "accelerometer 'none'; ambient-light-sensor 'none'; autoplay 'none'; battery 'none'; browsing-topics 'none'; camera 'none'; display-capture 'none'; domain-agent 'none'; document-domain 'none'; encrypted-media 'none'; execution-while-not-rendered 'none'; execution-while-out-of-viewport ''; gamepad 'none'; geolocation 'none'; gyroscope 'none'; hid 'none'; identity-credentials-get 'none'; idle-detection 'none'; local-fonts 'none'; magnetometer 'none'; microphone 'none'; midi 'none'; otp-credentials 'none'; payment 'none'; publickey-credentials-create 'none'; publickey-credentials-get 'none'; screen-wake-lock 'none'; serial 'none'; speaker-selection 'none'; usb 'none'; window-management 'none'; xr-spatial-tracking 'none'")
		dom.SetAttribute(embed, "csp", "sandbox allow-scripts allow-same-origin")
		bi.Embed = dom.OuterHTML(embed)
		bi.EmbedHostname = src.Hostname()
	case "hls":
		if bi.Resources["image"] == nil {
			return nil
		}
		*playerURL = *bi.videoPlayerURL
		playerURL.RawQuery = url.Values{
			"type": {"hls"},
			"src":  {src.String()},
			"w":    {strconv.Itoa(bi.Resources["image"].Width)},
			"h":    {strconv.Itoa(bi.Resources["image"].Height)},
		}.Encode()
		bi.Embed = fmt.Sprintf(
			`<iframe src="%s" width="%d" height="%d" frameborder="0" scrolling="no" sandbox="allow-scripts"></iframe>`,
			playerURL,
			bi.Resources["image"].Width,
			bi.Resources["image"].Height,
		)
	case "video":
		if bi.Resources["image"] == nil {
			return nil
		}
		*playerURL = *bi.videoPlayerURL
		playerURL.RawQuery = url.Values{
			"src": {src.String()},
			"w":   {strconv.Itoa(bi.Resources["image"].Width)},
			"h":   {strconv.Itoa(bi.Resources["image"].Height)},
		}.Encode()
		bi.Embed = fmt.Sprintf(
			`<iframe src="%s" width="%d" height="%d" frameborder="0" scrolling="no" sandbox="allow-scripts"></iframe>`,
			playerURL,
			bi.Resources["image"].Width,
			bi.Resources["image"].Height,
		)
	}

	return nil
}

// BookmarkSyncList is a list a [*BookmarkSync] items.
type BookmarkSyncList []*BookmarkSync

// NewBookmarkSyncList returns a new [BookmarkSyncList] from a queryset.
func NewBookmarkSyncList(_ context.Context, ds *goqu.SelectDataset) (BookmarkSyncList, error) {
	res := BookmarkSyncList{}
	for b, err := range scanner.Iter[BookmarkSync](ds) {
		if err != nil {
			return nil, err
		}
		b.Time = b.Time.UTC()
		res = append(res, b)
	}

	return res, nil
}

// GetLastModified implements [server.LastModer].
func (bsl BookmarkSyncList) GetLastModified() []time.Time {
	res := []time.Time{}
	for _, b := range bsl {
		res = append(res, b.Time)
	}
	return res
}

// UpdateEtag implements [server.Etagger].
func (bsl BookmarkSyncList) UpdateEtag(h hash.Hash) {
	for _, b := range bsl {
		io.WriteString(h, b.ID+strconv.FormatInt(b.Time.UTC().UnixNano(), 10))
	}
}

// BookmarkSync represent a bookmark's ID and last update time.
type BookmarkSync struct {
	ID   string    `json:"id" db:"uid"`
	Time time.Time `json:"time" db:"time"`
	Type string    `json:"type" db:"type"`
}

// SharedLink contains the publicly shared bookmark information.
type SharedLink struct {
	URL     string    `json:"url"`
	Expires time.Time `json:"expires"`
	Title   string    `json:"title"`
	ID      string    `json:"id"`
}

// SharedEmail contains the informat for sending a bookmark by email.
type SharedEmail struct {
	Form  forms.FormBinder
	Title string
	ID    string
	Error error
	Sent  bool
}
