// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2_test

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	. "codeberg.org/readeck/readeck/internal/testing" //revive:disable:dot-imports
)

func registerClient(t *testing.T, client *Client) string {
	r, err := client.NewRequest(http.MethodPost, "/api/oauth/client", map[string]any{
		"client_name":      "Test App",
		"client_uri":       "https://example.net/",
		"software_id":      uuid.NewString(),
		"software_version": "1.0.2",
		"redirect_uris":    []string{"http://[::1]:4000/callback"},
	})
	require.NoError(t, err)
	rsp := client.Request(t, r)
	rsp.AssertStatus(t, 201)
	return rsp.JSON.(map[string]any)["client_id"].(string)
}

func TestServerMetadata(t *testing.T) {
	app := NewTestApp(t)
	defer app.Close(t)

	app.Client().RT(t,
		WithTarget("/.well-known/oauth-authorization-server"),
		AssertStatus(200),
		AssertJSON(`{
			"issuer":"http://readeck.example.org/",
			"authorization_endpoint":"http://readeck.example.org/authorize",
			"token_endpoint":"http://readeck.example.org/api/oauth/token",
			"device_authorization_endpoint":"http://readeck.example.org/api/oauth/device",
			"registration_endpoint":"http://readeck.example.org/api/oauth/client",
			"revocation_endpoint": "http://readeck.example.org/api/oauth/revoke",
			"grant_types_supported":["authorization_code", "urn:ietf:params:oauth:grant-type:device_code"],
			"response_types_supported":["code"],
			"scopes_supported":["bookmarks:read","bookmarks:write","profile:read"],
			"code_challenge_methods_supported":["S256"],
			"token_endpoint_auth_methods_supported": ["none", "bearer"]
		}`),
	)
}

func TestAuthorizationCodeFlow(t *testing.T) {
	app := NewTestApp(t)
	defer app.Close(t)

	t.Run("authorization form", func(t *testing.T) {
		client := app.Client()
		user := app.Client(WithSession("user"))

		clientID := registerClient(t, client)

		codeVerifier := "210e967c91ae52d32bf414f1439769fb0eda1f828fc8d49c78e18ac5"
		h := sha256.New()
		h.Write([]byte(codeVerifier))

		codeChallenge := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
		params := url.Values{}
		params.Set("client_id", clientID)
		params.Set("redirect_uri", "http://[::1]:4000/callback")
		params.Set("scope", "bookmarks:read bookmarks:write")
		params.Set("state", "random.state")
		params.Set("code_challenge", codeChallenge)
		params.Set("code_challenge_method", "S256")

		client.RT(t,
			WithTarget("/authorize"),
			AssertStatus(303),
			AssertRedirect("/login"),
		)

		user.RT(t,
			WithName("authorize ko"),
			WithTarget("/authorize"),
			AssertStatus(401),
			AssertJSON(`{"error": "invalid_client"}`),
		)

		user.RT(t,
			WithName("authorization form"),
			WithTarget("/authorize?"+params.Encode()),
			AssertStatus(200),
			AssertContains("Authorize</button>"),
		)

		user.RT(t,
			WithName("authorize ok"),
			WithMethod(http.MethodPost),
			WithTarget(user.History.PrevURL()),
			WithBody(url.Values{"granted": {"1"}}),
			AssertStatus(http.StatusFound),
			WithAssert(func(t *testing.T, rsp *Response) {
				assert := require.New(t)
				location := rsp.Header.Get("location")
				assert.NotEmpty(location)
				u, err := url.Parse(location)
				assert.NoError(err)

				query := u.Query()
				assert.Contains(query, "code")
				assert.NotEmpty(query.Get("code"))
				assert.Equal("random.state", query.Get("state"))
			}),
		)

		user.RT(t,
			WithName("authorize deny"),
			WithMethod(http.MethodPost),
			WithTarget(user.History.PrevURL()),
			WithBody(url.Values{"granted": {"0"}}),
			AssertStatus(http.StatusFound),
			WithAssert(func(t *testing.T, rsp *Response) {
				assert := require.New(t)
				location := rsp.Header.Get("location")
				assert.NotEmpty(location)
				u, err := url.Parse(location)
				assert.NoError(err)

				query := u.Query()
				assert.Equal("access_denied", query.Get("error"))
				assert.Equal("access denied", query.Get("error_description"))
				assert.Contains(query, "code")
				assert.Empty(query.Get("code"))
			}),
		)

		user.RT(t,
			WithName("client gone after deny"),
			WithTarget(user.History.PrevURL()),
			AssertStatus(http.StatusUnauthorized),
		)
	})

	t.Run("token", func(t *testing.T) {
		client := app.Client()
		user := app.Client(WithSession("user"))
		clientID := registerClient(t, client)

		codeVerifier := "210e967c91ae52d32bf414f1439769fb0eda1f828fc8d49c78e18ac5"
		h := sha256.New()
		h.Write([]byte(codeVerifier))

		codeChallenge := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
		params := url.Values{}
		params.Set("client_id", clientID)
		params.Set("redirect_uri", "http://[::1]:4000/callback")
		params.Set("scope", "bookmarks:read bookmarks:write")
		params.Set("state", "random.state")
		params.Set("code_challenge", codeChallenge)
		params.Set("code_challenge_method", "S256")

		tokenCode := ""
		accessToken := ""

		user.RT(t,
			WithName("authorize form"),
			WithTarget("/authorize?"+params.Encode()),
			AssertContains(`Authorize</button>`),
		)

		user.RT(t,
			WithName("authorize ok"),
			WithMethod(http.MethodPost),
			WithTarget(user.History.PrevURL()),
			WithBody(url.Values{
				"granted": {"1"},
			}),
			AssertStatus(http.StatusFound),
			WithAssert(func(t *testing.T, rsp *Response) {
				u, err := url.Parse(rsp.Header.Get("location"))
				require.NoError(t, err)

				query := u.Query()
				tokenCode = query.Get("code")
			}),
		)

		client.RT(t,
			WithName("token challenge ko"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/token"),
			WithBody(url.Values{}),
			AssertStatus(400),
			AssertJSON(`{
				"error":"invalid_request",
				"error_description":"error on field \"grant_type\": field is required"
			}`),
		)

		client.RT(t,
			WithName("missing fields"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/token"),
			WithBody(url.Values{
				"grant_type": []string{"authorization_code"},
			}),
			AssertStatus(400),
			AssertJSON(`{
				"error":"invalid_request",
				"error_description":"error on field \"code\": field is required"
			}`),
		)

		client.RT(t,
			WithName("token challenge ko"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/token"),
			WithBody(url.Values{
				"grant_type":    []string{"authorization_code"},
				"code":          []string{"pnIEw47"},
				"code_verifier": []string{"FaRhB"},
			}),
			AssertStatus(400),
			AssertJSON(`{
				"error":"invalid_grant",
				"error_description":"code is not valid"
			}`),
		)

		client.RT(t,
			WithName("token challenge ko"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/token"),
			WithBody(url.Values{
				"grant_type":    []string{"authorization_code"},
				"code":          []string{tokenCode},
				"code_verifier": []string{"8GrkY4Fk1XpnIEw47w71XDEoMFaRhB8SvLhs9ZLCCNI"},
			}),
			AssertStatus(400),
			AssertJSON(`{
				"error":"invalid_grant",
				"error_description":"code is not valid"
			}`),
		)

		client.RT(t,
			WithName("token ok"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/token"),
			WithBody(url.Values{
				"grant_type":    []string{"authorization_code"},
				"code":          []string{tokenCode},
				"code_verifier": []string{codeVerifier},
			}),
			AssertStatus(http.StatusCreated),
			AssertJSON(`{
				"access_token": "<<PRESENCE>>",
				"id": "<<PRESENCE>>",
				"scope": "bookmarks:read bookmarks:write",
				"token_type": "Bearer"
			}`),
			WithAssert(func(t *testing.T, rsp *Response) {
				accessToken = rsp.JSON.(map[string]any)["access_token"].(string)
				require.Empty(t, Store().Get("oauth:client:"+clientID))
			}),
		)

		client.RT(t,
			WithName("profile with new token"),
			WithTarget("/api/profile"),
			WithHeader("Authorization", "Bearer "+accessToken),
			AssertStatus(200),
		)

		user.RT(t,
			WithName("revoke token with session auth"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/revoke"),
			WithBody(map[string]string{
				"token": accessToken,
			}),
			AssertStatus(400),
			AssertJSON(`{
				"error":"access_denied"
			}`),
		)

		app.Client(WithToken("user")).RT(t,
			WithName("revoke token with another token auth"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/revoke"),
			WithBody(map[string]string{
				"token": accessToken,
			}),
			AssertStatus(400),
			AssertJSON(`{
				"error":"access_denied"
			}`),
		)

		client.RT(t,
			WithName("revoke token"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/revoke"),
			WithHeader("Authorization", "Bearer "+accessToken),
			WithBody(map[string]string{
				"token": accessToken,
			}),
			AssertStatus(200),
		)

		client.RT(t,
			WithName("profile with revoked token"),
			WithTarget("/api/profile"),
			WithHeader("Authorization", "Bearer "+accessToken),
			AssertStatus(401),
		)

		client.RT(t,
			WithName("revoke token already revoked"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/revoke"),
			WithHeader("Authorization", "Bearer "+accessToken),
			WithBody(map[string]string{
				"token": accessToken,
			}),
			AssertStatus(401),
		)
	})
}

func TestDeviceCodeFlow(t *testing.T) {
	app := NewTestApp(t)
	defer app.Close(t)

	client := app.Client()
	user := app.Client(WithSession("user"))

	t.Run("granted access", func(t *testing.T) {
		clientID := registerClient(t, client)
		deviceCode := ""
		userCode := ""

		client.RT(t,
			WithName("device code request"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/device"),

			WithBody(url.Values{
				"client_id": {clientID},
				"scope":     {"bookmarks:read"},
			}),
			AssertStatus(200),
			AssertJSON(`{
				"device_code": "<<PRESENCE>>",
				"user_code": "<<PRESENCE>>",
				"verification_uri": "http://readeck.example.org/device",
				"verification_uri_complete": "<<PRESENCE>>",
				"expires_in": 300,
				"interval": 5
			}`),
			WithAssert(func(t *testing.T, rsp *Response) {
				deviceCode = rsp.JSON.(map[string]any)["device_code"].(string)
				userCode = rsp.JSON.(map[string]any)["user_code"].(string)
				require.NotEqual(t, deviceCode, userCode)

				fullURI := rsp.JSON.(map[string]any)["verification_uri_complete"].(string)
				require.Equal(t,
					"http://readeck.example.org/device?user_code="+userCode,
					fullURI,
				)

				require.NotEmpty(t, Store().Get("oauth:device-code:"+userCode))
			}),
		)

		client.RT(t,
			WithName("pending token"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/token"),
			WithBody(map[string]any{
				"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
				"device_code": deviceCode,
				"client_id":   clientID,
			}),
			AssertStatus(400),
			AssertJSON(`{"error": "authorization_pending"}`),
		)

		client.RT(t,
			WithName("slow down"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/token"),
			WithBody(map[string]any{
				"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
				"device_code": deviceCode,
				"client_id":   clientID,
			}),
			AssertStatus(400),
			AssertJSON(`{"error": "slow_down"}`),
		)

		user.RT(t,
			WithName("authorization page"),
			WithTarget("/device"),
			AssertContains("Enter the code displayed on your device"),
		)

		user.RT(t,
			WithName("authorization page"),
			WithTarget("/device?user_code="+userCode),
			AssertStatus(200),
			AssertContains("would like permission to access your account"),
		)

		user.RT(t,
			WithName("authorization page"),
			WithTarget("/device?user_code=abcd"),
			AssertStatus(400),
			AssertContains("This code has expired or is not valid"),
		)

		user.RT(t,
			WithName("grant authorization"),
			WithMethod(http.MethodPost),
			WithTarget("/device"),
			WithBody(url.Values{
				"user_code": {userCode},
				"granted":   {"1"},
			}),
			AssertStatus(http.StatusSeeOther),
			WithAssert(func(t *testing.T, rsp *Response) {
				require.Equal(t,
					"http://readeck.example.org/device?user_code="+userCode,
					rsp.Header.Get("Location"),
				)
			}),
		)

		user.RT(t,
			WithName("authorization granted"),
			WithTarget(user.History[0].Response.Redirect),
			AssertStatus(200),
			AssertContains("Authorization granted. Connecting device..."),
			WithAssert(func(t *testing.T, rsp *Response) {
				require.Equal(t, "6", rsp.Header.Get("Refresh"))
			}),
		)

		client.RT(t,
			WithName("invalid grant type"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/token"),
			WithBody(map[string]any{
				"grant_type":  "something",
				"device_code": deviceCode,
				"client_id":   clientID,
			}),
			AssertStatus(400),
			AssertJSON(`{
				"error":"invalid_request",
				"error_description":"error on field \"grant_type\": something is not one of \"authorization_code\", \"urn:ietf:params:oauth:grant-type:device_code\""
			}`),
		)

		client.RT(t,
			WithName("missing fields"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/token"),
			WithBody(map[string]any{
				"grant_type": "urn:ietf:params:oauth:grant-type:device_code",
			}),
			AssertStatus(400),
			AssertJSON(`{
				"error":"invalid_request",
				"error_description":"error on field \"client_id\": field is required"
			}`),
		)

		client.RT(t,
			WithName("retrieve token"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/token"),
			WithBody(map[string]any{
				"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
				"device_code": deviceCode,
				"client_id":   clientID,
			}),
			AssertStatus(201),
			AssertJSON(`{
				"id": "<<PRESENCE>>",
				"access_token": "<<PRESENCE>>",
				"token_type": "Bearer",
				"scope": "bookmarks:read"
			}`),
		)

		client.RT(t,
			WithName("profile with new token"),
			WithTarget("/api/profile"),
			WithHeader("Authorization",
				"Bearer "+client.History[0].Response.JSON.(map[string]any)["access_token"].(string)),
			AssertStatus(200),
		)
	})

	t.Run("denied access", func(t *testing.T) {
		clientID := registerClient(t, client)
		deviceCode := ""
		userCode := ""

		client.RT(t,
			WithName("device code request"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/device"),

			WithBody(url.Values{
				"client_id": {clientID},
				"scope":     {"bookmarks:read"},
			}),
			AssertStatus(200),
			WithAssert(func(_ *testing.T, rsp *Response) {
				deviceCode = rsp.JSON.(map[string]any)["device_code"].(string)
				userCode = rsp.JSON.(map[string]any)["user_code"].(string)
			}),
		)

		user.RT(t,
			WithName("deny authorization"),
			WithMethod(http.MethodPost),
			WithTarget("/device"),
			WithBody(url.Values{
				"user_code": {userCode},
				"granted":   {"0"},
			}),
			AssertStatus(http.StatusSeeOther),
			WithAssert(func(t *testing.T, rsp *Response) {
				require.Equal(t,
					"http://readeck.example.org/device?user_code="+userCode,
					rsp.Header.Get("Location"),
				)
			}),
		)

		user.RT(t,
			WithName("authorization denied"),
			WithTarget(user.History[0].Response.Redirect),
			AssertStatus(200),
			AssertContains("Authorization request was denied."),
			WithAssert(func(t *testing.T, rsp *Response) {
				require.Empty(t, rsp.Header.Get("Refresh"))
			}),
		)

		client.RT(t,
			WithName("token denied"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/token"),
			WithBody(map[string]any{
				"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
				"device_code": deviceCode,
				"client_id":   clientID,
			}),
			AssertStatus(400),
			AssertJSON(`{"error": "access_denied"}`),
		)
	})

	t.Run("expired code", func(t *testing.T) {
		clientID := registerClient(t, client)
		deviceCode := ""
		userCode := ""

		client.RT(t,
			WithName("device code request"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/device"),

			WithBody(url.Values{
				"client_id": {clientID},
				"scope":     {"bookmarks:read"},
			}),
			AssertStatus(200),
			WithAssert(func(_ *testing.T, rsp *Response) {
				deviceCode = rsp.JSON.(map[string]any)["device_code"].(string)
				userCode = rsp.JSON.(map[string]any)["user_code"].(string)
			}),
		)

		// Code has expired
		require.NoError(t, Store().Del("oauth:device-code:"+userCode))

		user.RT(t,
			WithName("expired authorization"),
			WithMethod(http.MethodPost),
			WithTarget("/device"),
			WithBody(url.Values{
				"user_code": {userCode},
				"granted":   {"1"},
			}),
			AssertStatus(400),
			AssertContains("This code has expired or is not valid"),
		)

		client.RT(t,
			WithName("token denied"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/token"),
			WithBody(map[string]any{
				"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
				"device_code": deviceCode,
				"client_id":   clientID,
			}),
			AssertStatus(400),
			AssertJSON(`{"error": "expired_token"}`),
		)
	})

	t.Run("scope error", func(t *testing.T) {
		clientID := registerClient(t, client)
		client.RT(t,
			WithName("device code request"),
			WithMethod(http.MethodPost),
			WithTarget("/api/oauth/device"),

			WithBody(url.Values{
				"client_id": {clientID},
				"scope":     {"abc   def ", "xyz"},
			}),
			AssertStatus(400),
			AssertJSON(`{
				"error": "invalid_scope",
				"error_description":"abc is not one of \"bookmarks:read\", \"bookmarks:write\", \"profile:read\", def is not one of \"bookmarks:read\", \"bookmarks:write\", \"profile:read\", xyz is not one of \"bookmarks:read\", \"bookmarks:write\", \"profile:read\""
			}`),
		)
	})
}

func TestClientRegistration(t *testing.T) {
	app := NewTestApp(t)
	defer app.Close(t)

	client := app.Client()

	t.Run("invalid redirect uri", func(t *testing.T) {
		tests := []string{
			"http://test.localhost/",
			"http://localhost/",
			"http://example.org/",
			"https://",
		}
		seq := []*RequestTest{}
		for i, test := range tests {
			seq = append(seq, RT(
				WithName(strconv.Itoa(i+1)),
				WithMethod(http.MethodPost),
				WithTarget("/api/oauth/client"),
				WithBody(map[string]any{
					"redirect_uris": []string{
						test,
					},
				}),
				AssertStatus(400),
				AssertJSON(`{
					"error":"invalid_redirect_uri",
					"error_description":"invalid URL"
				}`),
			))
		}

		client.Sequence(t, seq...)
	})

	t.Run("invalid client uri", func(t *testing.T) {
		tests := []string{
			"http://example.com/",
			"https://192.168.0.1:4000/test",
			"https://[fc00::1]/",
			"https://localhost/",
			"https://test.localhost/",
		}
		seq := []*RequestTest{}
		for i, test := range tests {
			seq = append(seq, RT(
				WithName(strconv.Itoa(i+1)),
				WithMethod(http.MethodPost),
				WithTarget("/api/oauth/client"),
				WithBody(map[string]any{
					"client_name":      "test",
					"client_uri":       test,
					"software_id":      "abc",
					"software_version": "1.0",
					"redirect_uris":    []string{"https://example.org/callback"},
				}),
				AssertStatus(400),
				AssertJSON(`{
					"error": "invalid_client_metadata",
					"error_description": "error on field \"client_uri\": invalid client URI"
				}`),
			))
		}

		client.Sequence(t, seq...)
	})

	t.Run("invalid logo uri", func(t *testing.T) {
		tests := []string{
			"something",
			"data:image/gif;base64,R0lGODlhAQABAAAAACwAAAAAAQABAAA=",
		}
		seq := []*RequestTest{}
		for i, test := range tests {
			seq = append(seq, RT(
				WithName(strconv.Itoa(i+1)),
				WithMethod(http.MethodPost),
				WithTarget("/api/oauth/client"),
				WithBody(map[string]any{
					"client_name":      "test",
					"client_uri":       "https://example.org/",
					"logo_uri":         test,
					"software_id":      "abc",
					"software_version": "1.0",
					"redirect_uris":    []string{"https://example.org/callback"},
				}),
				AssertStatus(400),
				AssertJSON(`{
					"error":"invalid_client_metadata",
					"error_description":"error on field \"logo_uri\": invalid logo URI"
				}`),
			))
		}

		client.Sequence(t, seq...)
	})

	client.RT(t,
		WithName("no client name"),
		WithMethod(http.MethodPost),
		WithTarget("/api/oauth/client"),
		WithBody(map[string]any{
			"redirect_uris": []string{"https://example.org/callback"},
		}),
		AssertStatus(400),
		AssertJSON(`{
			"error": "invalid_client_metadata",
			"error_description": "<<PRESENCE>>"
		}`),
	)

	client.RT(t,
		WithName("no client uri"),
		WithMethod(http.MethodPost),
		WithTarget("/api/oauth/client"),
		WithBody(map[string]any{
			"client_name":   "test",
			"redirect_uris": []string{"https://example.org/callback"},
		}),
		AssertStatus(400),
		AssertJSON(`{
			"error": "invalid_client_metadata",
			"error_description": "<<PRESENCE>>"
		}`),
	)

	client.RT(t,
		WithName("invalid grant types"),
		WithMethod(http.MethodPost),
		WithTarget("/api/oauth/client"),
		WithBody(map[string]any{
			"client_name":   "test",
			"client_uri":    "https://example.org/",
			"redirect_uris": []string{"https://example.org/callback"},
			"grant_types":   []string{"something"},
		}),
		AssertStatus(400),
		AssertJSON(`{
			"error": "invalid_client_metadata",
			"error_description":"error on field \"grant_types\": something is not one of \"authorization_code\", \"urn:ietf:params:oauth:grant-type:device_code\""
		}`),
	)

	client.RT(t,
		WithName("invalid auth method"),
		WithMethod(http.MethodPost),
		WithTarget("/api/oauth/client"),
		WithBody(map[string]any{
			"client_name":                "test",
			"client_uri":                 "https://example.org/",
			"software_id":                "abc",
			"software_version":           "1.0.0",
			"redirect_uris":              []string{"https://example.org/callback"},
			"token_endpoint_auth_method": "something",
		}),
		AssertStatus(400),
		AssertJSON(`{
			"error": "invalid_client_metadata",
			"error_description":"error on field \"token_endpoint_auth_method\": something is not one of \"none\""
		}`),
	)

	client.RT(t,
		WithName("invalid response types"),
		WithMethod(http.MethodPost),
		WithTarget("/api/oauth/client"),
		WithBody(map[string]any{
			"client_name":      "test",
			"client_uri":       "https://example.org/",
			"software_id":      "abc",
			"software_version": "1.0.0",
			"redirect_uris":    []string{"https://example.org/callback"},
			"response_types":   []string{"something"},
		}),
		AssertStatus(400),
		AssertJSON(`{
			"error": "invalid_client_metadata",
			"error_description":"error on field \"response_types\": something is not one of \"code\""
		}`),
	)

	client.RT(t,
		WithName("create client"),
		WithMethod(http.MethodPost),
		WithTarget("/api/oauth/client"),
		WithBody(map[string]any{
			"client_name":      "test",
			"client_uri":       "https://example.org/",
			"logo_uri":         "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+P+/HgAFhAJ/wlseKgAAAABJRU5ErkJggg==",
			"software_id":      "10098d11-8b3f-4ebb-b519-cab2301975fa",
			"software_version": "1.0.0",
			"redirect_uris": []string{
				"https://example.org/callback",
				"https://example.org/callback2",
				"net.myapp:oauth-callback",
				"net.myapp:///oauth-callback",
				"http://127.0.0.8:8000/callback",
				"http://[::1]:8000/callback",
			},
		}),
		AssertStatus(201),
		AssertJSON(`{
			"client_id":"<<PRESENCE>>",
			"client_name": "test",
			"client_uri": "https://example.org/",
			"logo_uri": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+P+/HgAFhAJ/wlseKgAAAABJRU5ErkJggg==",
			"redirect_uris":[
				"https://example.org/callback",
				"https://example.org/callback2",
				"net.myapp:oauth-callback",
				"net.myapp:///oauth-callback",
				"http://127.0.0.8:8000/callback",
				"http://[::1]:8000/callback"
			],
			"software_id":"10098d11-8b3f-4ebb-b519-cab2301975fa",
			"software_version":"1.0.0",
			"token_endpoint_auth_method":"none",
			"grant_types":["authorization_code","urn:ietf:params:oauth:grant-type:device_code"],
			"response_types":["code"]
		}`),
	)
}
