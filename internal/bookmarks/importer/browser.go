// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package importer

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-shiori/dom"
	"golang.org/x/net/html"

	"codeberg.org/readeck/readeck/internal/db/types"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

type browserAdapter struct {
	idx   int
	Items []browserBookmarkItem `json:"items"`
}

type browserAdapterForm struct {
	FileImportForm
	LabelsFromTitles forms.BooleanField `json:"labels_from_titles"`
}

type browserBookmarkItem struct {
	Link       string        `json:"url"`
	Title      string        `json:"title"`
	Created    time.Time     `json:"created"`
	Labels     types.Strings `json:"labels"`
	IsArchived bool          `json:"is_archived"`
}

func (bi *browserBookmarkItem) URL() string {
	return bi.Link
}

func (bi *browserBookmarkItem) Meta() (*BookmarkMeta, error) {
	return &BookmarkMeta{
		Title:      bi.Title,
		Created:    bi.Created,
		Labels:     bi.Labels,
		IsArchived: bi.IsArchived,
	}, nil
}

func (adapter *browserAdapter) Name(ctx context.Context) string {
	return forms.GetTranslator(ctx).Gettext("Browser Bookmarks")
}

func (adapter *browserAdapter) Form(ctx context.Context) importBinder {
	return forms.New[browserAdapterForm](ctx)
}

func (adapter *browserAdapter) Params(form forms.FormBinder) ([]byte, error) {
	if !form.IsValid() {
		return nil, nil
	}
	f := form.(*browserAdapterForm)

	reader, err := f.Data.Value().Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close() //nolint:errcheck

	root, err := html.Parse(reader)
	if err != nil {
		f.Data.AddErrors(forms.Gettext("Unable to read HTML content"), err)
		return nil, nil
	}

	for _, n := range dom.QuerySelectorAll(root, "dt > a[href]") {
		uri, err := url.Parse(dom.GetAttribute(n, "href"))
		if err != nil {
			continue
		}
		uri.Fragment = ""
		if !slices.Contains(allowedSchemes, uri.Scheme) {
			continue
		}

		if slices.ContainsFunc(adapter.Items, func(bi browserBookmarkItem) bool {
			return bi.Link == uri.String()
		}) {
			continue
		}

		item := browserBookmarkItem{
			Created: time.Now().UTC(),
			Link:    uri.String(),
			Title:   strings.TrimSpace(dom.TextContent(n)),
			Labels:  types.Strings{},
		}

		if dom.HasAttribute(n, "add_date") {
			if ts, err := strconv.Atoi(dom.GetAttribute(n, "add_date")); err == nil {
				item.Created = time.Unix(int64(ts), 0).UTC()
			}
		}

		// Fetch labels from the TAGS attribute when present (pinboard, maybe others)
		for label := range strings.SplitSeq(dom.GetAttribute(n, "tags"), ",") {
			if label = strings.TrimSpace(label); label != "" {
				item.Labels = append(item.Labels, label)
			}
		}

		// If there's a TOREAD attribute, use it to set IsArchived.
		if dom.HasAttribute(n, "toread") && dom.GetAttribute(n, "toread") == "0" {
			item.IsArchived = true
		}

		if f.LabelsFromTitles.Value() {
			// Fetch hierarchy titles and make them labels
			item.Labels = append(item.Labels, adapter.findNodeTitles(n))
		}

		adapter.Items = append(adapter.Items, item)
	}

	if len(adapter.Items) == 0 {
		f.Data.AddErrors(errInvalidFile)
		return nil, nil
	}

	slices.Reverse(adapter.Items)
	return json.Marshal(adapter)
}

func (adapter *browserAdapter) LoadData(data []byte) error {
	return json.Unmarshal(data, adapter)
}

func (adapter *browserAdapter) Next() (BookmarkImporter, error) {
	if adapter.idx+1 > len(adapter.Items) {
		return nil, io.EOF
	}

	adapter.idx++
	return &adapter.Items[adapter.idx-1], nil
}

// findNodeTitles returns a list of titles found on top of the current link node.
func (adapter *browserAdapter) findNodeTitles(node *html.Node) string {
	res := []string{}
	n := node.Parent
	for n != nil {
		// Walk all the way back to each node's parent.
		if dom.TagName(n) == "dl" {
			// A title is the previous sibling with an h3 tag.
			// The loop will get them in reverse order.
			if ps := dom.PreviousElementSibling(n); ps != nil && dom.TagName(ps) == "h3" {
				if title := strings.TrimSpace(dom.TextContent(ps)); title != "" {
					res = append(res, title)
				}
			}
		}

		n = n.Parent
	}

	slices.Reverse(res)
	return strings.Join(res, "/")
}
