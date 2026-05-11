// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package videoplayer_test

import (
	"net/url"
	"strconv"
	"testing"

	. "codeberg.org/readeck/readeck/internal/testing" //revive:disable:dot-imports
	"github.com/stretchr/testify/assert"
)

func TestVideoPlayer(t *testing.T) {
	app := NewTestApp(t)
	defer app.Close(t)
	client := app.Client()

	t.Run("invalid parameters", func(t *testing.T) {
		tests := []struct {
			params url.Values
			json   string
		}{
			{
				url.Values{},
				`{
					"is_valid": false,
					"errors": null,
					"fields": {
						"src": {
							"is_null": false,
							"is_bound": false,
							"value": "",
							"errors": ["field is required"]
						},
						"type": {
							"is_null": false,
							"is_bound": false,
							"value": "video",
							"errors": null
						},
						"w": {
							"is_null": false,
							"is_bound": false,
							"value": 0,
							"errors": null
						},
						"h": {
							"is_null": false,
							"is_bound": false,
							"value": 0,
							"errors": null
						}
					}
				}`,
			},
			{
				url.Values{
					"src":  {"https://example.org/"},
					"type": {"unknown"},
					"w":    {"-10"},
					"h":    {"-5"},
				},
				`{
					"is_valid": false,
					"errors": null,
					"fields": {
						"src": {
							"is_null": false,
							"is_bound": true,
							"value": "https://example.org/",
							"errors": null
						},
						"type": {
							"is_null": false,
							"is_bound": true,
							"value": "unknown",
							"errors": ["unknown is not one of \"hls\", \"embed\", \"video\""]
						},
						"w": {
							"is_null": false,
							"is_bound": true,
							"value": -10,
							"errors": ["must be greater or equal than 0"]
						},
						"h": {
							"is_null": false,
							"is_bound": true,
							"value": -5,
							"errors": ["must be greater or equal than 0"]
						}
					}
				}`,
			},
		}

		seq := []*RequestTest{}
		for i, test := range tests {
			seq = append(seq, RT(
				WithName(strconv.Itoa(i+1)),
				WithTarget("/videoplayer?"+test.params.Encode()),
				AssertStatus(422),
				AssertJSON(test.json),
			))
		}

		client.Sequence(t, seq...)
	})

	t.Run("video types", func(t *testing.T) {
		tests := []struct {
			t      string
			expect string
		}{
			{"video", `<video id="video" controls autoplay src="https://example.org/?v=abc" width="16" height="10"></video>`},
			{"embed", `<iframe src="https://example.org/?v=abc" width="16" height="10"`},
			{"hls", `<video id="video" controls data-manifest="https://example.org/?v=abc" width="16" height="10"></video>`},
		}

		seq := []*RequestTest{}
		for _, test := range tests {
			params := url.Values{"src": {"https://example.org/?v=abc"}, "type": {test.t}, "w": {"16"}, "h": {"10"}}
			seq = append(seq, RT(
				WithName(test.t),
				WithTarget("/videoplayer?"+params.Encode()),
				AssertStatus(200),
				AssertContains(test.expect),
				WithAssert(func(t *testing.T, rsp *Response) {
					csp := rsp.Header.Get("Content-Security-Policy")
					assert := assert.New(t)
					assert.Contains(csp, "connect-src example.org;")
					assert.Contains(csp, "media-src 'self' data: blob: example.org;")
					assert.Contains(csp, "frame-src blob: example.org;")
					assert.Contains(csp, "frame-ancestors 'self';")
					assert.Equal("SAMEORIGIN", rsp.Header.Get("X-Frame-Options"))
				}),
			))
		}
		client.Sequence(t, seq...)
	})
}
