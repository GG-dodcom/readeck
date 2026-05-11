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
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codeberg.org/readeck/readeck/internal/db/types"
	"codeberg.org/readeck/readeck/pkg/forms"
)

var fixtureFS = os.DirFS("fixtures")

type fileOpener interface {
	Read([]byte) (int, error)
	Close() error
}

type fileLoader interface {
	Open() (fileOpener, error)
}

type fixtureFile string

func (filename fixtureFile) Open() (fileOpener, error) {
	return fixtureFS.Open(string(filename))
}

type dataFile string

func (data dataFile) Open() (fileOpener, error) {
	return io.NopCloser(strings.NewReader(string(data))), nil
}

func loadFile(t *testing.T, file fileLoader, adapter ImportLoader) (forms.FormBinder, []BookmarkImporter) {
	require := require.New(t)
	fl, err := file.Open()
	require.NoError(err)
	defer fl.Close() //nolint:errcheck

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("data", "data")
	_, _ = io.Copy(part, fl)
	writer.Close() //nolint:errcheck

	req, _ := http.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	f := adapter.Form(context.Background())
	forms.Bind(req, f)

	if !f.IsValid() {
		return f, nil
	}

	data, err := adapter.Params(f)
	require.NoError(err)

	if !f.IsValid() {
		return f, nil
	}

	err = adapter.(ImportWorker).LoadData(data)
	require.NoError(err)

	res := []BookmarkImporter{}
	for {
		bi, err := adapter.(ImportWorker).Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(err)
		res = append(res, bi)
	}

	return f, res
}

type fileTest struct {
	file      fileLoader
	formError string
	expected  string
}

type bookmarkItem struct {
	URL  string
	Meta *BookmarkMeta
}

func testFileAdapter(t *testing.T, adapterName string, tests []fileTest) {
	t.Setenv("TZ", "Europe/Paris")

	for i, test := range tests {
		t.Run(strconv.Itoa(i+1), func(t *testing.T) {
			require := require.New(t)
			adapter := LoadAdapter(adapterName)
			f, items := loadFile(t, test.file, adapter)
			field := f.Fields()["data"]

			if test.formError != "" {
				require.Equal(test.formError, field.Errors().Error())
			} else {
				require.Empty(field.Errors().Error())
				require.True(f.IsValid())
			}

			var err error
			res := make([]bookmarkItem, len(items))
			for i, x := range items {
				v := bookmarkItem{URL: x.URL()}
				if x, ok := x.(BookmarkEnhancer); ok {
					v.Meta, err = x.Meta()
					require.NoError(err)
				}
				res[i] = v
			}

			buf := new(bytes.Buffer)
			enc := json.NewEncoder(buf)
			enc.SetIndent("", "  ")
			require.NoError(enc.Encode(res))

			if !assert.JSONEq(t, test.expected, buf.String()) {
				t.Log(buf.String())
				t.FailNow()
			}
		})
	}
}

func TestBrowser(t *testing.T) {
	testFileAdapter(t, "browser", []fileTest{
		{
			dataFile(""),
			"field is required",
			"[]",
		},
		{
			dataFile("  "),
			"Empty or invalid import file",
			"[]",
		},
		{
			fixtureFile("browser.html"),
			"",
			`[
				{
					"URL": "https://example.org/",
					"Meta": {
						"Title": "Example.org",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [],
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2012-11-30T11:05:29Z"
					}
				},
				{
					"URL": "https://example.net/",
					"Meta": {
						"Title": "Example.net",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [],
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2013-11-26T10:38:19Z"
					}
				},
				{
					"URL": "http://blog.mozilla.com/",
					"Meta": {
						"Title": "Mozilla News",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [
							"mozilla",
							"blog",
							"test"
						],
						"IsArchived": true,
						"IsMarked": false,
						"Created": "2020-09-29T20:32:45Z"
					}
				},
				{
					"URL": "https://www.mozilla.org/en-US/firefox/central/",
					"Meta": {
						"Title": "Getting Started",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [],
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2019-12-18T07:09:39Z"
					}
				}
			]`,
		},
	})
}

func TestCSV(t *testing.T) {
	testFileAdapter(t, "csv", []fileTest{
		{
			dataFile("  "),
			"Empty or invalid import file",
			"[]",
		},
		{
			dataFile("url,title"),
			"Empty or invalid import file",
			"[]",
		},
		{
			dataFile("{}"),
			"Empty or invalid import file",
			"[]",
		},
		{
			dataFile("url,title\n" + "https://example.net/,test,123\n"),
			"",
			`[
				{
					"URL": "https://example.net/",
					"Meta": {
						"Title": "test",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": null,
						"IsArchived": false,
						"IsMarked": false,
						"Created": "0001-01-01T00:00:00Z"
					}
				}
			]`,
		},
		{
			fixtureFile("csv-simple.csv"),
			"",
			`[
				{
					"URL": "https://www.linuxserver.io/",
					"Meta": {
						"Title": "Some Title",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [
							"test label",
							"label B"
						],
						"IsArchived": true,
						"IsMarked": false,
						"Created": "2025-02-01T15:21:43Z"
					}
				},
				{
					"URL": "https://www.startpage.com/",
					"Meta": {
						"Title": "Site Title",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": null,
						"IsArchived": false,
						"IsMarked": false,
						"Created": "0001-01-01T00:00:00Z"
					}
				}
			]`,
		},
		{
			fixtureFile("csv-instapaper.csv"),
			"",
			`[
				{
					"URL": "https://www.newyorker.com/business/currency/the-gnu-manifesto-turns-thirty",
					"Meta": {
						"Title": "Richard Stallman’s GNU Manifesto Turns Thirty | The New Yorker",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": null,
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2023-06-20T23:08:30Z"
					}
				},
				{
					"URL": "https://css-irl.info/dont-forget-the-lang-attribute/",
					"Meta": {
						"Title": "CSS { In Real Life } | Don’t Forget the “lang” Attribute",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": null,
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2021-12-21T10:01:19Z"
					}
				}
			]`,
		},
	})
}

func TestGoodLinks(t *testing.T) {
	testFileAdapter(t, "goodlinks", []fileTest{
		{
			dataFile("  "),
			"Empty or invalid import file",
			"[]",
		},
		{
			fixtureFile("goodlinks.json"),
			"",
			`[
				{
					"URL": "https://www.startpage.com/",
					"Meta": {
						"Title": "Shodan",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [
							"search"
						],
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2020-05-04T14:12:42Z"
					}
				},
				{
					"URL": "https://www.linuxserver.io/",
					"Meta": {
						"Title": "Home | LinuxServer.io",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [
							"linux",
							"docker"
						],
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2020-05-16T09:30:18Z"
					}
				}
			]`,
		},
	})
}

func TestPinboard(t *testing.T) {
	testFileAdapter(t, "pinboard", []fileTest{
		{
			dataFile("  "),
			"Empty or invalid import file",
			"[]",
		},
		{
			fixtureFile("pinboard.json"),
			"",
			`[
				{
					"URL": "https://codeberg.org/readeck/readeck#under-the-hood",
					"Meta": {
						"Title": "readeck/readeck: Readeck is a simple web application that lets you save the precious readable content of web pages you like and want to keep forever. - Codeberg.org",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "a longer free-form text",
						"Embed": "",
						"Labels": [],
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2026-03-01T13:28:24Z"
					}
				},
				{
					"URL": "https://www.enisa.europa.eu/publications/the-enisa-cybersecurity-exercise-methodology",
					"Meta": {
						"Title": "The ENISA Cybersecurity Exercise Methodology | ENISA",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [
							"security",
							"foo",
							"bar"
						],
						"IsArchived": true,
						"IsMarked": false,
						"Created": "2026-02-28T12:47:30Z"
					}
				}
			]`,
		},
	})
}

func TestLinkwarden(t *testing.T) {
	testFileAdapter(t, "linkwarden", []fileTest{
		{
			dataFile("  "),
			"Empty or invalid import file",
			"[]",
		},
		{
			dataFile("[]"),
			"Empty or invalid import file",
			"[]",
		},
		{
			fixtureFile("linkwarden.json"),
			"",
			`[
				{
					"URL": "https://www.the-reframe.com/the-neighborhood/",
					"Meta": {
						"Title": "The Neighborhood - by A.R. Moxon - The Reframe",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [
							"label b"
						],
						"IsArchived": false,
						"IsMarked": true,
						"Created": "2025-06-24T05:24:31.751Z"
					}
				},
				{
					"URL": "https://www.youtube.com/watch?v=Wp8ux8Xlj48",
					"Meta": {
						"Title": "You're Living On An Ant Planet - YouTube",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [
							"label a"
						],
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2025-06-24T05:25:23.231Z"
					}
				},
				{
					"URL": "https://upload.wikimedia.org/wikipedia/commons/1/15/King's_Cross_Western_Concourse.jpg",
					"Meta": {
						"Title": "",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [
							"label a"
						],
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2025-06-24T05:26:24.397Z"
					}
				}
			]`,
		},
	})
}

func TestPocket(t *testing.T) {
	testFileAdapter(t, "pocket-file", []fileTest{
		{
			dataFile("  "),
			"Empty or invalid import file",
			"[]",
		},
		{
			fixtureFile("pocket.zip"),
			"",
			`[
				{
					"URL": "https://example.org/read",
					"Meta": {
						"Title": "Read article",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [],
						"IsArchived": true,
						"IsMarked": false,
						"Created": "2024-04-02T05:59:04Z"
					}
				},
				{
					"URL": "https://example.org/",
					"Meta": {
						"Title": "Example.net",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [
							"tag1",
							"tag2"
						],
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2023-05-24T07:29:06Z"
					}
				},
				{
					"URL": "https://example.net/",
					"Meta": {
						"Title": "Example.net",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [],
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2023-05-24T07:32:02Z"
					}
				}
			]`,
		},
		{
			fixtureFile("pocket_part.csv"),
			"",
			`[
				{
					"URL": "https://example.org/read",
					"Meta": {
						"Title": "Read article",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [],
						"IsArchived": true,
						"IsMarked": false,
						"Created": "2024-04-02T05:59:04Z"
					}
				},
				{
					"URL": "https://example.org/",
					"Meta": {
						"Title": "Example.net",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [
							"tag1",
							"tag2"
						],
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2023-05-24T07:29:06Z"
					}
				},
				{
					"URL": "https://example.net/",
					"Meta": {
						"Title": "Example.net",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [],
						"IsArchived": false,
						"IsMarked": false,
						"Created": "2023-05-24T07:32:02Z"
					}
				}
			]`,
		},
	})
}

func TestReadwise(t *testing.T) {
	testFileAdapter(t, "readwise", []fileTest{
		{
			dataFile("  "),
			"Empty or invalid import file",
			"[]",
		},
		{
			fixtureFile("readwise.csv"),
			"",
			`[
				{
					"URL": "https://www.linuxserver.io/",
					"Meta": {
						"Title": "Some Title",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": [
							"don't",
							"peanut, butter"
						],
						"IsArchived": false,
						"IsMarked": true,
						"Created": "2022-12-05T12:42:10Z"
					}
				},
				{
					"URL": "https://www.startpage.com/",
					"Meta": {
						"Title": "",
						"Published": "0001-01-01T00:00:00Z",
						"Authors": null,
						"Lang": "",
						"TextDirection": "",
						"DocumentType": "",
						"Description": "",
						"Embed": "",
						"Labels": null,
						"IsArchived": true,
						"IsMarked": false,
						"Created": "2025-01-20T22:13:02.447Z"
					}
				}
			]`,
		},
	})
}

func TestText(t *testing.T) {
	testFileAdapter(t, "text", []fileTest{
		{
			dataFile("foo\n"),
			"Empty or invalid import file",
			"[]",
		},
		{
			fixtureFile("text.txt"),
			"",
			`[
				{
					"URL": "https://example.net/",
					"Meta": null
				},
				{
					"URL": "https://example.org/",
					"Meta": null
				}
			]`,
		},
	})
}

func TestOmnivore(t *testing.T) {
	t.Setenv("TZ", "Europe/Paris")

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	httpmock.RegisterResponder(
		"POST",
		"/api/graphql",
		func(r *http.Request) (*http.Response, error) {
			token := r.Header.Get("Authorization")

			var payload struct {
				Query         string         `json:"query"`
				OperationName string         `json:"operationName"`
				Variables     map[string]any `json:"variables"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				return nil, err
			}

			switch {
			case payload.OperationName == "" && strings.HasPrefix(payload.Query, "query Viewer{me"):
				if token == "failed" {
					return httpmock.NewJsonResponse(500, map[string]any{
						"errors": []any{},
					})
				}
				return httpmock.NewJsonResponse(200, map[string]any{
					"data": map[string]any{
						"me": map[string]any{
							"id":   "abc",
							"name": "alice",
						},
					},
				})
			case payload.OperationName == "Search":
				after, _ := strconv.Atoi(payload.Variables["after"].(string))
				first := int(payload.Variables["first"].(float64))

				items := []map[string]any{}
				for x := range 25 {
					node := map[string]any{
						"id":          strconv.Itoa(after + x),
						"title":       fmt.Sprintf("Article %d", after+x),
						"url":         fmt.Sprintf("https://example.net/article-%d", after+x),
						"createdAt":   "2024-01-02 12:23:43",
						"publishedAt": "2022-01-02 12:23:43",
						"content":     fmt.Sprintf("<p>Some content %d</p>", after+x),
						"pageType":    "ARTICLE",
						"author":      "",
						"image":       fmt.Sprintf("https://example.net/picture-%d.webp", after+x),
						"siteIcon":    "https://example.net/icon.png",
						"description": fmt.Sprintf("Description %d", after+x),
						"labels":      []any{},
						"state":       "SUCCEEDED",
					}
					if after+x == 0 {
						node["author"] = "Someone"
						node["state"] = "ARCHIVED"
						node["labels"] = []map[string]string{
							{"name": "label 1"}, {"name": "label 2"},
						}
					}

					items = append(items, map[string]any{
						"cursor": strconv.Itoa(after + first),
						"node":   node,
					})
				}
				response := map[string]any{
					"data": map[string]any{
						"search": map[string]any{
							"edges": items,
							"pageInfo": map[string]any{
								"hasNextPage": after < 60,
								"startCursor": strconv.Itoa(after),
								"endCursor":   strconv.Itoa(after + first),
							},
						},
					},
				}

				return httpmock.NewJsonResponse(200, response)
			}

			return httpmock.NewJsonResponse(200, nil)
		},
	)

	t.Run("auth failed", func(t *testing.T) {
		adapter := LoadAdapter("omnivore")
		f := adapter.Form(context.Background()).(*omnivoreAPIAdapterForm)
		forms.BindValues(url.Values{
			"url":   {"https://omnivore.app/"},
			"token": {"failed"},
		}, f)
		f.Bind()

		require := require.New(t)

		_, err := adapter.Params(f)
		require.NoError(err)
		require.False(f.IsValid())
		require.EqualError(f.Token.Errors(), "Invalid API Key")
	})

	t.Run("auth ok", func(t *testing.T) {
		adapter := LoadAdapter("omnivore")
		f := adapter.Form(context.Background()).(*omnivoreAPIAdapterForm)
		forms.BindValues(url.Values{
			"url":   {"https://omnivore.app/"},
			"token": {"abcd"},
		}, f)

		require := require.New(t)

		_, err := adapter.Params(f)
		require.NoError(err)
		require.True(f.IsValid())
	})

	t.Run("import", func(t *testing.T) {
		adapter := LoadAdapter("omnivore")
		f := adapter.Form(context.Background()).(*omnivoreAPIAdapterForm)
		forms.BindValues(url.Values{
			"url":   {"https://omnivore.app/"},
			"token": {"abcd"},
		}, f)

		require := require.New(t)

		data, err := adapter.Params(f)
		require.NoError(err)
		require.True(f.IsValid())

		worker := adapter.(ImportWorker)
		err = worker.LoadData(data)
		require.NoError(err)

		i := -1
		for {
			i++
			item, err := worker.Next()
			if err == io.EOF {
				break
			}
			require.NoError(err)

			require.Equal(fmt.Sprintf("https://example.net/article-%d", i), item.URL())
			bi, err := item.(BookmarkEnhancer).Meta()
			require.NoError(err)

			require.Equal(fmt.Sprintf("Article %d", i), bi.Title)
			require.Equal(fmt.Sprintf("Description %d", i), bi.Description)
			require.Equal(time.Date(2024, time.January, 2, 12, 23, 43, 0, time.UTC), bi.Created)
			require.Equal(time.Date(2022, time.January, 2, 12, 23, 43, 0, time.UTC), bi.Published)

			if i == 0 {
				require.Equal(types.Strings{"Someone"}, bi.Authors)
				require.Equal(types.Strings{"label 1", "label 2"}, bi.Labels)
				require.True(bi.IsArchived)
			} else {
				require.False(bi.IsArchived)
			}

			resources := item.(BookmarkResourceProvider).Resources()
			require.Len(resources, 1)
			require.Equal(
				fmt.Sprintf(
					`<html><head><meta property="og:image" content="https://example.net/picture-%d.webp"/><link rel="icon" href="https://example.net/icon.png"/></head><body><p>Some content %d</p></body></html>`,
					i, i,
				),
				string(resources[0].Data),
			)
		}
	})
}

func TestWallabag(t *testing.T) {
	t.Setenv("TZ", "Europe/Paris")

	adapter := LoadAdapter("wallabag")
	f := adapter.Form(context.Background()).(*wallabagAdapterForm)
	forms.BindValues(url.Values{
		"url":           {"https://wallabag/"},
		"username":      {"user"},
		"password":      {"pass"},
		"client_id":     {"client_id"},
		"client_secret": {"client_secret"},
	}, f)

	f.Bind()

	httpmock.Activate()
	defer httpmock.DeactivateAndReset()

	httpmock.RegisterResponder("POST", "/oauth/v2/token", httpmock.NewJsonResponderOrPanic(
		http.StatusOK,
		map[string]string{
			"access_token": "1234",
		},
	))

	httpmock.RegisterRegexpResponder("GET", regexp.MustCompile(`^/api/entries\?`), func(r *http.Request) (*http.Response, error) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))

		var next map[string]string
		if page < 5 {
			q := r.URL.Query()
			q.Set("page", strconv.Itoa(page+1))
			r.URL.RawQuery = q.Encode()
			next = map[string]string{
				"href": r.URL.String(),
			}
		}

		response := map[string]any{
			"_links": map[string]any{
				"next": next,
			},
		}

		items := []map[string]any{}
		for _, x := range []string{"a", "b", "c"} {
			headers := map[string]string{}
			switch x {
			case "a":
				headers["content-type"] = "application/xml"
			case "b":
				headers["content-type"] = "application/xhtml xml; charset=utf-8"
			case "c":
				headers["content-type"] = "text/html; charset=koi8-r"
			}
			items = append(items, map[string]any{
				"is_archived":     0,
				"is_starred":      0,
				"title":           fmt.Sprintf("Article <i>%d&#47;%s</i>", page, x),
				"url":             fmt.Sprintf("https://example.net/%d/article-%s#ignored", page, x),
				"content":         fmt.Sprintf("<p>some content %d - %s</p>", page, x),
				"created_at":      "2024-01-02 12:23:43",
				"published_at":    "2022-01-02 12:23:43",
				"published_by":    []string{},
				"language":        "en",
				"tags":            []string{},
				"preview_picture": fmt.Sprintf("https://example.net/picture-%d%s.webp", page, x),
				"headers":         headers,
			})
		}
		response["_embedded"] = map[string]any{
			"items": items,
		}

		return httpmock.NewJsonResponse(200, response)
	})

	require := require.New(t)

	data, err := adapter.Params(f)
	require.NoError(err)
	require.True(f.IsValid())
	require.JSONEq(`{"url":"https://wallabag","token":"1234"}`, string(data))

	worker := adapter.(ImportWorker)
	err = worker.LoadData(data)
	require.NoError(err)

	i := 0
	letters := []string{"a", "b", "c"}
	for {
		item, err := worker.Next()
		if err == io.EOF {
			break
		}
		require.NoError(err)

		page := 1 + i/3
		x := letters[i%3]
		i++

		require.Equal(fmt.Sprintf("https://example.net/%d/article-%s", page, x), item.URL())
		bi, err := item.(BookmarkEnhancer).Meta()
		require.NoError(err)

		require.Equal(fmt.Sprintf("Article %d/%s", page, x), bi.Title)
		require.Equal(time.Date(2024, time.January, 2, 12, 23, 43, 0, time.UTC), bi.Created)
		require.Equal(time.Date(2022, time.January, 2, 12, 23, 43, 0, time.UTC), bi.Published)

		resources := item.(BookmarkResourceProvider).Resources()
		require.Len(resources, 1)

		require.Equal(
			fmt.Sprintf(
				`<html><head><meta property="og:image" content="https://example.net/picture-%d%s.webp"/></head><body><p>some content %d - %s</p></body></html>`,
				page, x, page, x,
			),
			string(resources[0].Data),
		)

		require.Equal("text/html; charset=utf-8", resources[0].Header.Get("content-type"))
	}
}
