// SPDX-FileCopyrightText: © 2023 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package docs_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	. "codeberg.org/readeck/readeck/internal/testing" //revive:disable:dot-imports
)

func TestPermissions(t *testing.T) {
	app := NewTestApp(t)
	defer func() {
		app.Close(t)
	}()

	users := []string{"admin", "staff", "user", "disabled", ""}
	for _, user := range users {
		app.Client(WithSession(user)).Sequence(t,
			RT(
				WithTarget("/docs"),
				WithAssert(func(t *testing.T, rsp *Response) {
					switch user {
					case "admin", "staff", "user":
						rsp.AssertStatus(t, 303)
						rsp.AssertRedirect(t, "/docs/en/")
					case "disabled":
						rsp.AssertStatus(t, 403)
					case "":
						rsp.AssertStatus(t, 303)
						rsp.AssertRedirect(t, "/login")
					}
				}),
			),
			RT(
				WithTarget("/docs/bookmark"),
				WithAssert(func(t *testing.T, rsp *Response) {
					switch user {
					case "admin", "staff", "user":
						rsp.AssertStatus(t, 303)
						rsp.AssertRedirect(t, "/docs/en/bookmark")
					case "disabled":
						rsp.AssertStatus(t, 403)
					case "":
						rsp.AssertStatus(t, 303)
						rsp.AssertRedirect(t, "/login")
					}
				}),
			),
			RT(
				WithTarget("/docs/en/"),
				WithAssert(func(t *testing.T, rsp *Response) {
					switch user {
					case "admin", "staff", "user":
						rsp.AssertStatus(t, 200)
					case "disabled":
						rsp.AssertStatus(t, 403)
					case "":
						rsp.AssertStatus(t, 303)
						rsp.AssertRedirect(t, "/login")
					}
				}),
			),
			RT(
				WithTarget("/docs/en/bookmark"),
				WithAssert(func(t *testing.T, rsp *Response) {
					switch user {
					case "admin", "staff", "user":
						rsp.AssertStatus(t, 200)
					case "disabled":
						rsp.AssertStatus(t, 403)
					case "":
						rsp.AssertStatus(t, 303)
						rsp.AssertRedirect(t, "/login")
					}
				}),
			),
			RT(
				WithTarget("/docs/en/not-found"),
				AssertStatus(404),
			),
			RT(
				WithTarget("/docs/en/img/bookmark-new.webp"),
				AssertStatus(200),
				WithAssert(func(t *testing.T, rsp *Response) {
					require.Equal(t, "image/webp", rsp.Header.Get("content-type"))
				}),
			),
			RT(
				WithTarget("/docs/about"),
				WithAssert(func(t *testing.T, rsp *Response) {
					switch user {
					case "admin", "staff":
						rsp.AssertStatus(t, 200)
					case "user", "disabled":
						rsp.AssertStatus(t, 403)
					case "":
						rsp.AssertStatus(t, 303)
						rsp.AssertRedirect(t, "/login")
					}
				}),
			),
			RT(
				WithTarget("/docs/api"),
				WithAssert(func(t *testing.T, rsp *Response) {
					switch user {
					case "admin", "staff", "user":
						rsp.AssertStatus(t, 200)
					case "disabled":
						rsp.AssertStatus(t, 403)
					case "":
						rsp.AssertStatus(t, 303)
						rsp.AssertRedirect(t, "/login")
					}
				}),
			),
			RT(
				WithTarget("/docs/api.json"),
				WithAssert(func(t *testing.T, rsp *Response) {
					switch user {
					case "admin", "staff", "user":
						rsp.AssertStatus(t, 200)
						require.Equal(t, "application/json", rsp.Header.Get("content-type"))
					case "disabled":
						rsp.AssertStatus(t, 403)
					case "":
						rsp.AssertStatus(t, 303)
						rsp.AssertRedirect(t, "/login")
					}
				}),
			),
		)
	}
}
