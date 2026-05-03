// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package users_test

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/pkg/forms/v2"

	. "codeberg.org/readeck/readeck/internal/testing" //revive:disable:dot-imports
)

type userForm struct {
	forms.Form
	Username forms.TextField `json:"username" validate:"trim is_valid_username"`
	Email    forms.TextField `json:"email"    validate:"trim is_valid_email"`
}

func TestUserEmailValidators(t *testing.T) {
	app := NewTestApp(t)
	defer app.Close(t)

	tests := []struct {
		name     string
		username *string
		email    *string
		expected string
	}{
		{
			name:     "ok values",
			username: new("test"),
			email:    new("test@example.org"),
			expected: `{
				"is_valid": true,
				"errors": null,
				"fields": {
					"email": {
						"is_null": false,
						"is_bound": true,
						"value": "test@example.org",
						"errors": null
					},
					"username": {
						"is_null": false,
						"is_bound": true,
						"value": "test",
						"errors": null
					}
				}
			}`,
		},

		{
			name:     "valid username as email",
			username: new("alice@example.org"),
			email:    new("alice@example.org"),
			expected: `{
				"is_valid": true,
				"errors": null,
				"fields": {
					"email": {
						"is_null": false,
						"is_bound": true,
						"value": "alice@example.org",
						"errors": null
					},
					"username": {
						"is_null": false,
						"is_bound": true,
						"value": "alice@example.org",
						"errors": null
					}
				}
			}`,
		},
		{
			name:     "invalid username as email",
			username: new("alice@example.org"),
			email:    new("alice@example.net"),
			expected: `{
				"is_valid": false,
				"errors": null,
				"fields": {
					"email": {
						"is_null": false,
						"is_bound": true,
						"value": "alice@example.net",
						"errors": null
					},
					"username": {
						"is_null": false,
						"is_bound": true,
						"value": "alice@example.org",
						"errors": ["username is not valid"]
					}
				}
			}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := forms.New[userForm](context.Background())
			values := url.Values{}
			if test.username != nil {
				values["username"] = []string{*test.username}
			}
			if test.email != nil {
				values["email"] = []string{*test.email}
			}

			forms.BindValues(values, f)

			sb := new(strings.Builder)
			require.NoError(t, json.NewEncoder(sb).Encode(f))

			if !assert.JSONEq(t, test.expected, sb.String()) {
				t.Log(sb.String())
			}
		})
	}
}

func TestBaseUserFormValidators(t *testing.T) {
	app := NewTestApp(t)
	defer app.Close(t)

	configs.Config.Accounts.UsernameDenyList = []string{
		"admin*",
		"root",
		"*test*",
	}
	configs.Config.Accounts.EmailDenyList = []string{
		"test@*",
		"*@example.net",
		"*@localhost",
		"*@*.localhost",
	}
	defer func() {
		configs.Config.Accounts.UsernameDenyList = []string{}
		configs.Config.Accounts.EmailDenyList = []string{}
	}()

	t.Run("username", func(t *testing.T) {
		tests := []struct {
			value string
			err   error
		}{
			{"a", users.ErrInvalidUsername},
			{"ab", users.ErrInvalidUsername},
			{"abc", nil},
			{"alice@example.org", nil},
			{"al ice", users.ErrInvalidUsername},
			{"al\nice", users.ErrInvalidUsername},
			{"al\u3000ice", users.ErrInvalidUsername},
			{"Al\u200Bice", users.ErrInvalidUsername},
			{"Al\x1dice", users.ErrInvalidUsername},
			{"Al\u00ADice", users.ErrInvalidUsername},
			{"ålice", nil},
			{"alice", nil},
			{"1alice", nil},
			{"admin", users.ErrBlockedUsername},
			{"administrator", users.ErrBlockedUsername},
			{"root", users.ErrBlockedUsername},
			{"abtest", users.ErrBlockedUsername},
			{"test-ab", users.ErrBlockedUsername},
		}

		for i, test := range tests {
			t.Run(strconv.Itoa(i+1), func(t *testing.T) {
				f := forms.New[userForm](context.Background())
				forms.BindValues(url.Values{"email": {"alice@example.org"}, "username": {test.value}}, f)

				if test.err == nil {
					require.True(t, f.IsValid())
					return
				}

				require.False(t, f.IsValid())
				require.ErrorIs(t, f.Username.Errors(), test.err)
			})
		}
	})

	t.Run("email", func(t *testing.T) {
		tests := []struct {
			value string
			err   error
		}{
			{"alice@example.com", nil},
			{"alice", forms.ErrInvalidEmail},
			{"alice@example.net", users.ErrBlockedEmailAddr},
			{"alice@localhost", users.ErrBlockedEmailAddr},
			{"alice@host.localhost", users.ErrBlockedEmailAddr},
			{"test@example.com", users.ErrBlockedEmailAddr},
		}

		for i, test := range tests {
			t.Run(strconv.Itoa(i+1), func(t *testing.T) {
				f := forms.New[userForm](context.Background())
				forms.BindValues(url.Values{"email": {test.value}, "username": {"alice"}}, f)

				if test.err == nil {
					require.True(t, f.IsValid())
					return
				}

				require.False(t, f.IsValid())
				require.ErrorIs(t, f.Email.Errors(), test.err)
			})
		}
	})
}
