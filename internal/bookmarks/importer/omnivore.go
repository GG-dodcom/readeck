// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/araddon/dateparse"
	"github.com/go-shiori/dom"
	"golang.org/x/net/html"

	"codeberg.org/readeck/readeck/internal/bookmarks/tasks"
	"codeberg.org/readeck/readeck/internal/db/types"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

type omnivoreAPIAdapter struct {
	Endpoint string `json:"url"`
	Token    string `json:"token"`
	articles *omnivoreAPISearchResult
}

type omnivoreAPIAdapterForm struct {
	BaseImportForm
	URL   forms.TextField `json:"url"   validate:"trim required max_len:128 is_url"`
	Token forms.TextField `json:"token" validate:"trim required max_len:64"`
}

type omnivoreAPISearchResult struct {
	Data struct {
		Search struct {
			Edges []struct {
				Cursor string          `json:"cursor"`
				Node   omnivoreAPINode `json:"node"`
			} `json:"edges"`
			PageInfo struct {
				HasNextPage     bool   `json:"hasNextPage"`
				HasPreviousPage bool   `json:"hasPreviousPage"`
				StartCursor     string `json:"startCursor"`
				EndCursor       string `json:"endCursor"`
				TotalCount      int    `json:"totalCount"`
			} `json:"pageInfo"`
		} `json:"search"`
	} `json:"data"`
}

type omnivoreAPINode struct {
	Title       string             `json:"title"`
	ItemURL     string             `json:"url"`
	PageType    string             `json:"pageType"`
	CreatedAt   string             `json:"createdAt"`
	Author      string             `json:"author"`
	Image       string             `json:"image"`
	Icon        string             `json:"siteIcon"`
	Description string             `json:"description"`
	PublishedAt string             `json:"publishedAt"`
	Labels      []omnivoreAPILabel `json:"labels"`
	State       string             `json:"state"`
	HTML        string             `json:"content"`
}

type omnivoreAPILabel struct {
	Label string `json:"name"`
}

func (n *omnivoreAPINode) URL() string {
	return n.ItemURL
}

func (n *omnivoreAPINode) Meta() (*BookmarkMeta, error) {
	res := &BookmarkMeta{
		Title:       n.Title,
		Authors:     types.Strings{n.Author},
		Description: n.Description,
		IsArchived:  n.State == "ARCHIVED",
		Labels:      types.Strings{},
	}

	res.Created, _ = dateparse.ParseAny(n.CreatedAt)
	res.Published, _ = dateparse.ParseAny(n.PublishedAt)

	if n.PageType != "ARTICLE" {
		res.Title = ""
	}

	for _, x := range n.Labels {
		res.Labels = append(res.Labels, x.Label)
	}

	return res, nil
}

func (n *omnivoreAPINode) EnableReadability() bool {
	return n.PageType != "ARTICLE" || n.HTML == ""
}

func (n *omnivoreAPINode) Resources() []tasks.MultipartResource {
	if n.EnableReadability() {
		return nil
	}

	root, err := html.Parse(strings.NewReader(n.HTML))
	if err != nil {
		return nil
	}

	if n.Image != "" {
		node := dom.CreateElement("meta")
		dom.SetAttribute(node, "property", "og:image")
		dom.SetAttribute(node, "content", n.Image)
		dom.QuerySelector(root, "head").AppendChild(node)
	}
	if n.Icon != "" {
		node := dom.CreateElement("link")
		dom.SetAttribute(node, "rel", "icon")
		dom.SetAttribute(node, "href", n.Icon)
		dom.QuerySelector(root, "head").AppendChild(node)
	}

	buf := new(bytes.Buffer)
	html.Render(buf, root)

	return []tasks.MultipartResource{
		{
			URL:  n.ItemURL,
			Data: buf.Bytes(),
			Header: http.Header{
				"Content-Type": {"text/html; charset=utf-8"},
			},
		},
	}
}

func (adapter *omnivoreAPIAdapter) Name(context.Context) string {
	return "Omnivore"
}

func (adapter *omnivoreAPIAdapter) Form(ctx context.Context) importBinder {
	f := forms.New[omnivoreAPIAdapterForm](ctx)
	f.URL.Set("https://omnivore.app/")

	return f
}

func (adapter *omnivoreAPIAdapter) Params(form forms.FormBinder) ([]byte, error) {
	if !form.IsValid() {
		return nil, nil
	}
	f := form.(*omnivoreAPIAdapterForm)

	endpoint, _ := url.Parse(f.URL.Value())
	endpoint.Fragment = ""
	endpoint = endpoint.JoinPath("/api/graphql")

	adapter.Endpoint = endpoint.String()
	adapter.Token = f.Token.Value()

	err := adapter.checkToken(f)
	if !f.IsValid() {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return json.Marshal(adapter)
}

func (adapter *omnivoreAPIAdapter) LoadData(data []byte) (err error) {
	if err = json.Unmarshal(data, adapter); err != nil {
		return
	}

	if adapter.Token == "" {
		err = errors.New("no token provided")
		return
	}

	adapter.articles = &omnivoreAPISearchResult{}
	adapter.articles.Data.Search.PageInfo.HasNextPage = true
	adapter.articles.Data.Search.PageInfo.EndCursor = "0"

	return
}

func (adapter *omnivoreAPIAdapter) Next() (BookmarkImporter, error) {
	var err error

	if len(adapter.articles.Data.Search.Edges) == 0 {
		if !adapter.articles.Data.Search.PageInfo.HasNextPage {
			// No next page, we're done
			return nil, io.EOF
		}

		// Fetch next article list
		if err = adapter.fetchArticles(25, adapter.articles.Data.Search.PageInfo.EndCursor); err != nil {
			return nil, errors.Join(io.EOF, err)
		}
	}

	// Pull the first item in the list
	item := adapter.articles.Data.Search.Edges[0]
	adapter.articles.Data.Search.Edges = adapter.articles.Data.Search.Edges[1:]

	// Cleanup the URL. This is done later by createBookmark() but
	// we want the URL to match anything that is sent by Resources() later.
	uri, err := url.Parse(item.Node.ItemURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrIgnore, err)
	}

	if !slices.Contains(allowedSchemes, uri.Scheme) {
		return nil, fmt.Errorf("%w: invalid scheme %s (%s)", ErrIgnore, uri.Scheme, uri)
	}

	item.Node.ItemURL = uri.String()

	return &item.Node, nil
}

func (adapter *omnivoreAPIAdapter) checkToken(f *omnivoreAPIAdapterForm) error {
	body := `{"query":"query Viewer{me {id name}}"}`
	req, _ := http.NewRequest(http.MethodPost, adapter.Endpoint, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", adapter.Token)

	rsp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.AddErrors(err)
		return nil
	}
	defer rsp.Body.Close() //nolint:errcheck

	if rsp.StatusCode != http.StatusOK {
		f.Token.AddErrors(forms.Gettext("Invalid API Key"))
		return nil
	}

	return nil
}

func (adapter *omnivoreAPIAdapter) fetchArticles(first int, after string) error {
	payload := map[string]any{
		"operationName": "Search",
		"variables": map[string]any{
			"first": first,
			"after": after,
		},
		"query": `
          query Search(
            $after: String
            $first: Int
          ) {
            search(
              first: $first
              after: $after
              query: "in:all"
              includeContent: true
            ) {
              ... on SearchSuccess {
                edges {
                  cursor
                  node {
                    id
                    title
                    slug
                    url
                    folder
                    pageType
                    contentReader
                    createdAt
                    readingProgressPercent
                    readingProgressTopPercent
                    readingProgressAnchorIndex
                    author
                    image
                    description
                    publishedAt
                    ownedByViewer
                    originalArticleUrl
                    uploadFileId
                    labels {
                      id
                      name
                      color
                    }
                    pageId
                    shortId
                    quote
                    annotation
                    state
                    siteName
                    siteIcon
                    subscription
                    readAt
                    savedAt
                    wordsCount
                    highlightsCount
                    content
                  }
                }
                pageInfo {
                  hasNextPage
                  hasPreviousPage
                  startCursor
                  endCursor
                  totalCount
                }
              }
              ... on SearchError {
                errorCodes
              }
            }
          }
		`,
	}

	body := new(bytes.Buffer)
	_ = json.NewEncoder(body).Encode(payload)

	req, _ := http.NewRequest(http.MethodPost, adapter.Endpoint, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", adapter.Token)

	rsp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer rsp.Body.Close() //nolint:errcheck

	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid response code (%d)", rsp.StatusCode)
	}

	err = json.NewDecoder(rsp.Body).Decode(adapter.articles)
	if len(adapter.articles.Data.Search.Edges) == 0 {
		return io.EOF
	}

	return err
}
