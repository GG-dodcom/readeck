// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package signin_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/stretchr/testify/require"

	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/pkg/totp"

	. "codeberg.org/readeck/readeck/internal/testing" //revive:disable:dot-imports
)

func TestSignin(t *testing.T) {
	app := NewTestApp(t)
	defer app.Close(t)

	t.Run("login view", func(t *testing.T) {
		type loginTest struct {
			user          string
			password      string
			loginStatus   int
			profileStatus int
		}
		tests := []loginTest{
			{"", "", 422, 303},
			{"admin", "admin", 303, 200},
			{"admin@localhost", "admin", 303, 200},
			{"user", "user", 303, 200},
			{"user@localhost", "user", 303, 200},
			{"disabled", "disabled", 303, 403},
			{"admin", "nope", 401, 303},
		}

		for _, test := range tests {
			t.Run(test.user, func(t *testing.T) {
				client := app.Client()

				client.RT(t,
					WithTarget("/"),
					AssertStatus(303),
					AssertRedirect("/login"),
				)

				client.RT(t,
					WithTarget("/login"),
					AssertStatus(200),
				)

				client.RT(t,
					WithMethod(http.MethodPost),
					WithTarget("/login"),
					WithBody(url.Values{
						"username": {test.user},
						"password": {test.password},
					}),
					AssertStatus(test.loginStatus),
					AssertRedirect(func() string {
						if test.loginStatus == 303 {
							return "/"
						}
						return ""
					}()),
				)

				client.RT(t,
					WithTarget("/profile"),
					AssertStatus(test.profileStatus),
				)
			})
		}
	})

	t.Run("last_login update", func(t *testing.T) {
		client := app.Client()
		now := time.Now().UTC()

		client.RT(t,
			WithMethod(http.MethodPost),
			WithTarget("/login"),
			WithBody(url.Values{
				"username": {"user"},
				"password": {"user"},
			}),
			AssertStatus(303),
			WithAssert(func(t *testing.T, _ *Response) {
				user, _ := users.Users.GetOne(goqu.C("username").Eq("user"))
				require.True(t, user.LastLogin.After(now))
			}),
		)
	})

	t.Run("logout view", func(t *testing.T) {
		client := app.Client()
		client.RT(t,
			WithMethod(http.MethodPost),
			WithTarget("/logout"),
			WithBody(url.Values{}),
			AssertStatus(303),
			AssertRedirect("/login"),
		)

		WithSession("user")(client)
		client.RT(t,
			WithTarget("/profile"),
			AssertStatus(200),
			WithAssert(func(t *testing.T, rsp *Response) {
				require.Len(t, client.Jar.Cookies(rsp.URL), 1)
			}),
		)

		client.RT(t,
			WithMethod(http.MethodPost),
			WithTarget("/logout"),
			WithBody(url.Values{}),
			AssertStatus(303),
			AssertRedirect("/"),
			WithAssert(func(t *testing.T, rsp *Response) {
				require.Empty(t, client.Jar.Cookies(rsp.URL))
			}),
		)

		client.RT(t,
			WithTarget("/"),
			AssertStatus(303),
			AssertRedirect("/login"),
		)
	})

	t.Run("totp ok", func(t *testing.T) {
		code := totp.Generate()

		require.NoError(t, app.Users["user"].User.SetTOTPCode(&code))
		require.NoError(t, app.Users["user"].User.Save())

		defer func() {
			app.Users["user"].User.TOTPSecret = nil
			require.NoError(t, app.Users["user"].Reset())
		}()

		client := app.Client()

		client.RT(t,
			WithMethod(http.MethodPost),
			WithTarget("/login"),
			WithBody(url.Values{
				"username": {"user"},
				"password": {"user"},
			}),
			AssertStatus(303),
			AssertRedirect("/login/mfa"),
		)

		client.RT(t,
			WithTarget(client.History[0].Response.Redirect),
			AssertStatus(200),
			AssertContains("Two-factor authentication"),
		)

		otp, err := code.OTP(time.Now().UTC())
		require.NoError(t, err)

		client.RT(t,
			WithMethod(http.MethodPost),
			WithTarget(client.History.PrevURL()),
			WithBody(url.Values{"code": {otp}}),
			AssertStatus(303),
			AssertRedirect(`^/$`),
		)

		// client is now logged-in
		client.RT(t,
			WithTarget("/profile"),
			AssertStatus(200),
		)
	})

	t.Run("totp ko", func(t *testing.T) {
		code := totp.Generate()

		tests := []struct {
			name   string
			code   func() (string, error)
			assert []TestOption
		}{
			{
				"too late",
				func() (string, error) { return code.OTP(time.Now().UTC().Add(2 * time.Minute)) },
				[]TestOption{
					AssertStatus(422),
					AssertContains("Invalid code"),
				},
			},
			{
				"wrong code",
				func() (string, error) { return totp.Generate().OTP(time.Now().UTC()) },
				[]TestOption{
					AssertStatus(422),
					AssertContains("Invalid code"),
				},
			},
			{
				"no code",
				func() (string, error) { return "", nil },
				[]TestOption{
					AssertStatus(422),
					AssertContains("field is required"),
				},
			},
			{
				"code too short",
				func() (string, error) { return "1111", nil },
				[]TestOption{
					AssertStatus(422),
					AssertContains("text must contain 6 characters"),
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				require.NoError(t, app.Users["user"].User.SetTOTPCode(&code))
				require.NoError(t, app.Users["user"].User.Save())

				defer func() {
					app.Users["user"].User.TOTPSecret = nil
					require.NoError(t, app.Users["user"].Reset())
				}()

				client := app.Client()
				client.RT(t,
					WithMethod(http.MethodPost),
					WithTarget("/login"),
					WithBody(url.Values{
						"username": {"user"},
						"password": {"user"},
					}),
					AssertStatus(303),
					AssertRedirect("/login/mfa"),
				)

				otp, err := test.code()
				require.NoError(t, err)

				client.RT(t,
					append([]TestOption{
						WithMethod(http.MethodPost),
						WithTarget(client.History[0].Response.Redirect),
						WithBody(url.Values{"code": {otp}}),
					}, test.assert...)...,
				)

				client.RT(t,
					WithTarget("/profile"),
					AssertStatus(303),
					AssertRedirect("/login"),
				)
			})
		}
	})
}
