// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package admin_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	. "codeberg.org/readeck/readeck/internal/testing" //revive:disable:dot-imports
)

func TestAPI(t *testing.T) {
	app := NewTestApp(t)
	defer func() {
		app.Close(t)
	}()

	client := app.Client(WithToken("admin"))
	u1, err := NewTestUser("test1", "test1@localhost", "test1", "user")
	require.NoError(t, err)

	t.Run("users", func(t *testing.T) {
		client.RT(t,
			WithTarget("/api/admin/users"),
			AssertStatus(200),
			AssertJSON(`[
				{
					"id": "<<PRESENCE>>",
					"href": "<<PRESENCE>>",
					"created": "<<PRESENCE>>",
					"updated": "<<PRESENCE>>",
					"last_login": "<<PRESENCE>>",
					"username": "admin",
					"email": "admin@localhost",
					"group": "admin",
					"is_deleted": false
				},
				{
					"id": "<<PRESENCE>>",
					"href": "<<PRESENCE>>",
					"created": "<<PRESENCE>>",
					"updated": "<<PRESENCE>>",
					"last_login": "<<PRESENCE>>",
					"username": "disabled",
					"email": "disabled@localhost",
					"group": "none",
					"is_deleted": false
				},
				{
					"id": "<<PRESENCE>>",
					"href": "<<PRESENCE>>",
					"created": "<<PRESENCE>>",
					"updated": "<<PRESENCE>>",
					"last_login": "<<PRESENCE>>",
					"username": "staff",
					"email": "staff@localhost",
					"group": "staff",
					"is_deleted": false
				},
				{
					"id": "<<PRESENCE>>",
					"href": "<<PRESENCE>>",
					"created": "<<PRESENCE>>",
					"updated": "<<PRESENCE>>",
					"last_login": "<<PRESENCE>>",
					"username": "test1",
					"email": "test1@localhost",
					"group": "user",
					"is_deleted": false
				},
				{
					"id": "<<PRESENCE>>",
					"href": "<<PRESENCE>>",
					"created": "<<PRESENCE>>",
					"updated": "<<PRESENCE>>",
					"last_login": "<<PRESENCE>>",
					"username": "user",
					"email": "user@localhost",
					"group": "user",
					"is_deleted": false
				}
			]`),
		)

		client.RT(t,
			WithTarget("/api/admin/users/"+u1.User.UID),
			AssertStatus(200),
			AssertJSON(`{
				"id": "<<PRESENCE>>",
				"href": "<<PRESENCE>>",
				"created": "<<PRESENCE>>",
				"updated": "<<PRESENCE>>",
				"last_login": "<<PRESENCE>>",
				"last_activity": "<<PRESENCE>>",
				"bookmark_count": 0,
				"bookmark_disk_usage": 0,
				"username": "test1",
				"email": "test1@localhost",
				"group": "user",
				"is_deleted": false,
				"settings": "<<PRESENCE>>"
			}`),
		)

		client.RT(t,
			WithTarget("/api/admin/users/sdfgsgsgergergerge"),
			AssertStatus(404),
			AssertJSON(`{"status":404,"message":"Not Found"}`),
		)

		client.RT(t,
			WithMethod(http.MethodPost),
			WithTarget("/api/admin/users"),
			WithBody(map[string]any{}),
			AssertStatus(422),
			AssertJSON(`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"email": {
						"is_bound": false,
						"is_null": false,
						"value": "",
						"errors": [
							"field is required"
						]
					},
					"group": {
						"is_bound": false,
						"is_null": false,
						"value": "user",
						"errors": ["field is required"]
					},
					"password": {
						"is_bound": false,
						"is_null": false,
						"value": "",
						"errors": [
							"field is required"
						]
					},
					"username": {
						"is_bound": false,
						"is_null": false,
						"value": "",
						"errors": [
							"field is required"
						]
					}
				}
			}`),
		)

		client.RT(t,
			WithMethod(http.MethodPost),
			WithTarget("/api/admin/users"),
			WithBody(map[string]any{
				"group": "foo",
			}),
			AssertStatus(422),
			AssertJSON(`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"email": {
						"is_bound": false,
						"is_null": false,
						"value": "",
						"errors": [
							"field is required"
						]
					},
					"group": {
						"is_bound": true,
						"is_null": false,
						"value": "foo",
						"errors": ["foo is not one of \"none\", \"admin\", \"staff\", \"user\""]
					},
					"password": {
						"is_bound": false,
						"is_null": false,
						"value": "",
						"errors": [
							"field is required"
						]
					},
					"username": {
						"is_bound": false,
						"is_null": false,
						"value": "",
						"errors": [
							"field is required"
						]
					}
				}
			}`),
		)

		client.RT(t,
			WithMethod(http.MethodPost),
			WithTarget("/api/admin/users"),
			WithBody(map[string]any{
				"username": "test3@localhost",
				"email":    "test3",
				"group":    "user",
				"password": "  ",
			}),
			AssertStatus(422),
			AssertJSON(`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"email": {
						"is_bound": true,
						"is_null": false,
						"value": "test3",
						"errors":[
							"not a valid email address"
						]
					},
					"group": {
						"is_bound": true,
						"is_null": false,
						"value": "user",
						"errors": null
					},
					"password": {
						"is_bound": true,
						"is_null": false,
						"value": "  ",
						"errors": ["password is empty"]
					},
					"username": {
						"is_bound": true,
						"is_null": false,
						"value": "test3@localhost",
						"errors":[
							"username is not valid"
						]
					}
				}
			}`),
		)

		client.RT(t,
			WithMethod(http.MethodPost),
			WithTarget("/api/admin/users"),
			WithBody(map[string]any{
				"username": "user",
				"email":    "test2@localhost",
				"group":    "user",
				"password": "1234",
			}),
			AssertStatus(422),
			AssertJSON(`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"email": {
						"is_bound": true,
						"is_null": false,
						"value": "test2@localhost",
						"errors": null
					},
					"group": {
						"is_bound": true,
						"is_null": false,
						"value": "user",
						"errors": null
					},
					"password": {
						"is_bound": true,
						"is_null": false,
						"value": "1234",
						"errors": null
					},
					"username": {
						"is_bound": true,
						"is_null": false,
						"value": "user",
						"errors": [
							"username is already in use"
						]
					}
				}
			}`),
		)

		client.RT(t,
			WithMethod(http.MethodPost),
			WithTarget("/api/admin/users"),
			WithBody(map[string]any{
				"username": "test2",
				"email":    "user@localhost",
				"group":    "user",
				"password": "1234",
			}),
			AssertStatus(422),
			AssertJSON(`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"email": {
						"is_bound": true,
						"is_null": false,
						"value": "user@localhost",
						"errors": ["email address is already in use"]
					},
					"group": {
						"is_bound": true,
						"is_null": false,
						"value": "user",
						"errors": null
					},
					"password": {
						"is_bound": true,
						"is_null": false,
						"value": "1234",
						"errors": null
					},
					"username": {
						"is_bound": true,
						"is_null": false,
						"value": "test2",
						"errors": null
					}
				}
			}`),
		)

		client.RT(t,
			WithMethod(http.MethodPost),
			WithTarget("/api/admin/users"),
			WithBody(map[string]any{
				"username": "test2@example.org",
				"email":    "test2@localhost",
				"group":    "user",
				"password": "1234",
			}),
			AssertStatus(422),
			AssertJSON(`{
				"is_valid": false,
				"errors": null,
				"fields": {
					"email": {
						"is_bound": true,
						"is_null": false,
						"value": "test2@localhost",
						"errors": null
					},
					"group": {
						"is_bound": true,
						"is_null": false,
						"value": "user",
						"errors": null
					},
					"password": {
						"is_bound": true,
						"is_null": false,
						"value": "1234",
						"errors": null
					},
					"username": {
						"is_bound": true,
						"is_null": false,
						"value": "test2@example.org",
						"errors": ["username is not valid"]
					}
				}
			}`),
		)

		client.RT(t,
			WithMethod(http.MethodPost),
			WithTarget("/api/admin/users"),
			WithBody(map[string]any{
				"username": "test2",
				"email":    "test2@localhost",
				"group":    "user",
				"password": "1234",
			}),
			AssertStatus(201),
			AssertJSON(`{"status":201,"message":"User created"}`),
		)

		client.RT(t,
			WithMethod(http.MethodPost),
			WithTarget("/api/admin/users"),
			WithBody(map[string]any{
				"username": "test-eq@localhost",
				"email":    "test-eq@localhost",
				"group":    "user",
				"password": "1234",
			}),
			AssertStatus(201),
			AssertJSON(`{"status":201,"message":"User created"}`),
		)

		client.RT(t,
			WithMethod(http.MethodPatch),
			WithTarget("/api/admin/users/"+u1.User.UID),
			WithBody(map[string]any{}),
			AssertStatus(200),
			AssertJSON(`{"id": "<<PRESENCE>>"}`),
		)

		client.RT(t,
			WithMethod(http.MethodPatch),
			WithTarget("/api/admin/users/"+u1.User.UID),
			WithBody(map[string]any{
				"username": "test3@localhost",
				"email":    "test3",
				"group":    "user",
				"password": "2345",
			}),
			AssertStatus(422),
			AssertJSON(`{
				"is_valid":false,
				"errors":null,
				"fields":{
					"email":{
						"is_null":false,
						"is_bound":true,
						"value":"test3",
						"errors":[
							"not a valid email address"
						]
					},
					"group":{
						"is_null":false,
						"is_bound":true,
						"value":"user",
						"errors":null
					},
					"password":{
						"is_null":false,
						"is_bound":true,
						"value":"2345",
						"errors":null
					},
					"username":{
						"is_null":false,
						"is_bound":true,
						"value":"test3@localhost",
						"errors":[
							"username is not valid"
						]
					}
				}
			}`),
		)

		client.RT(t,
			WithMethod(http.MethodPatch),
			WithTarget("/api/admin/users/"+u1.User.UID),
			WithBody(map[string]any{
				"username": "test3",
				"email":    "test3@localhost",
				"group":    "user",
				"password": "    ",
			}),
			AssertStatus(422),
			AssertJSON(`{
				"is_valid":false,
				"errors":null,
				"fields":{
					"email":{
						"is_null":false,
						"is_bound":true,
						"value":"test3@localhost",
						"errors":null
					},
					"group":{
						"is_null":false,
						"is_bound":true,
						"value":"user",
						"errors":null
					},
					"password":{
						"is_null":false,
						"is_bound":true,
						"value":"    ",
						"errors":["password is empty"]
					},
					"username":{
						"is_null":false,
						"is_bound":true,
						"value":"test3",
						"errors":null
					}
				}
			}`),
		)

		client.RT(t,
			WithMethod(http.MethodPatch),
			WithTarget("/api/admin/users/"+u1.User.UID),
			WithBody(map[string]any{
				"username": "test3",
				"email":    "test3@localhost",
				"group":    "user",
				"password": "2345",
			}),
			AssertStatus(200),
			AssertJSON(`{
				"id": "<<PRESENCE>>",
				"email": "test3@localhost",
				"group": "user",
				"password": "-",
				"updated": "<<PRESENCE>>",
				"username": "test3"
			}`),
		)

		client.RT(t,
			WithMethod(http.MethodDelete),
			WithTarget("/api/admin/users/"+u1.User.UID),
			AssertStatus(204),
		)

		client.RT(t,
			WithMethod(http.MethodDelete),
			WithTarget("/api/admin/users/"+app.Users["admin"].User.UID),
			AssertStatus(409),
			AssertJSON(`{
				"status": 409,
				"message": "same user as authenticated"
			}`),
		)
	})
}
