// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/auth/tokens"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/bus"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/forms"
)

const (
	deviceCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"
	deviceCodeTTL      = 300 // in seconds
	deviceCodeInterval = 5   // in seconds
)

const (
	codeRequestPending = "pending"
	codeRequestDenied  = "denied"
	codeRequestGranted = "granted"
)

type deviceViewRouter struct {
	chi.Router
}

func newDeviceViewRouter() *deviceViewRouter {
	router := &deviceViewRouter{
		server.AuthenticatedRouter(server.WithRedirectLogin),
	}

	router.Get("/", router.authorizeHandler)
	router.Post("/", router.authorizeHandler)

	return router
}

type deviceCodeStep uint8

const (
	stepCode deviceCodeStep = iota
	stepPending
	stepGranted
	stepDenied
	stepInvalid
)

type userCode string

type deviceAuthorizationResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// deviceAuthorizationRequest is where we store the current
// device code request. It contains everything that's needed
// to perform the flow, from the initial client's request to
// user approval.
// The request is stored in Readeck's key/value store with
// a TTL of 5 minutes.
type deviceAuthorizationRequest struct {
	ClientID    string    `json:"c"`
	UserID      int       `json:"u"`
	TokenID     int       `json:"t"`
	Expires     time.Time `json:"e"`
	LastChecked time.Time `json:"lc"`
	Status      string    `json:"s"`
	Scopes      []string  `json:"sc"`
}

func (r *deviceAuthorizationRequest) store(code userCode) error {
	return bus.Set("oauth:device-code:"+string(code), r, r.Expires.Sub(time.Now().UTC()))
}

func loadDeviceAuthorizationRequest(code userCode) (*deviceAuthorizationRequest, error) {
	r := &deviceAuthorizationRequest{}
	if err := bus.Get("oauth:device-code:"+string(code), r); err != nil {
		return nil, errServerError.withError(err)
	}
	return r, nil
}

// newUserCode generate a random [userCode] of 8 letters from [deviceCodeAlphabet].
func newUserCode() userCode {
	uc := make([]byte, 8)
	rand.Read(uc)
	for i := range uc {
		uc[i] = deviceCodeAlphabet[uc[i]%20]
	}

	return userCode(uc)
}

// userCodeFromDeviceCode loads a [userCode] from an encoded
// device code.
func userCodeFromDeviceCode(c string) (userCode, error) {
	data, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", err
	}

	content, err := configs.Keys.OauthRequestKey().Decode(data)
	if err != nil {
		return "", err
	}

	return userCode(content), nil
}

// toDeviceCode returns the code used as the device code.
// This is the symetric encoding of the [userCode] with an
// internal key.
func (c userCode) toDeviceCode() (string, error) {
	data, err := configs.Keys.OauthRequestKey().Encode([]byte(c))
	if err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(data), nil
}

// authorizeHandler is the view that presents the device code flow to
// the end user. Depending on the request status, it performs different
// tasks.
func (h *deviceViewRouter) authorizeHandler(w http.ResponseWriter, r *http.Request) {
	f := forms.BindAs[deviceAuthorizationForm](r)

	var err error
	status := http.StatusOK
	scopes := []string{}
	step := stepCode
	var client *oauthClient
	var req *deviceAuthorizationRequest

	if f.UserCode.Value() != "" {
		code := userCode(f.UserCode.Value())
		req, err = loadDeviceAuthorizationRequest(code)
		if err != nil {
			status = http.StatusBadRequest
			step = stepInvalid
			goto RENDER
		}

		switch req.Status {
		case codeRequestPending:
			client, err = loadClient(req.ClientID, grantTypeDeviceCode)
			if err != nil {
				server.Err(w, r, err)
				return
			}

			step = stepPending

			availableScopes := users.GroupList(server.Locale(r), "@oauth_scope", auth.GetRequestUser(r))
			for _, x := range availableScopes {
				if slices.Contains(req.Scopes, x[0]) {
					scopes = append(scopes, x[1])
				}
			}

			if r.Method == http.MethodPost {
				if f.Granted.IsBound() {
					if f.Granted.Value() {
						req.Status = codeRequestGranted
						req.UserID = auth.GetRequestUser(r).ID
					} else {
						req.Status = codeRequestDenied
					}

					if err := req.store(code); err != nil {
						server.Err(w, r, err)
						return
					}

					// update status and redirect
					params := url.Values{"user_code": []string{string(code)}}
					redir := urls.AbsoluteURL(r, "")
					redir.RawQuery = params.Encode()
					w.Header().Set("Location", redir.String())
					w.WriteHeader(http.StatusSeeOther)
					return
				}
			}
			goto RENDER
		case codeRequestGranted:
			step = stepGranted
			if req.TokenID < 0 {
				// Refresh the page until the token is created
				w.Header().Set("Refresh", "6")
			}
			goto RENDER
		case codeRequestDenied:
			step = stepDenied
			goto RENDER
		default:
			status = http.StatusBadRequest
			step = stepInvalid
		}
	}

RENDER:
	server.RenderComponent(w, r, status, Views{}.deviceCode(
		step, f, client, req, scopes,
	))
}

// deviceHandler is the handler that creates a new device code and
// returns [deviceAuthorizationResponse] as a JSON response.
func (api *oauthAPI) deviceHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")

	f := forms.BindAs[deviceForm](r)

	if !f.IsValid() {
		server.Err(w, r, f.getError())
		return
	}

	client, err := loadClient(f.ClientID.Value(), grantTypeDeviceCode)
	if err != nil {
		server.Err(w, r, err)
		return
	}

	code := newUserCode()
	deviceCode, err := code.toDeviceCode()
	if err != nil {
		server.Err(w, r, errServerError.withError(err))
		return
	}

	verificationURI := urls.AbsoluteURL(r, "/device")
	verificationURIComplete := new(url.URL)
	*verificationURIComplete = *verificationURI

	q := verificationURIComplete.Query()
	q.Add("user_code", string(code))
	verificationURIComplete.RawQuery = q.Encode()

	if err := (&deviceAuthorizationRequest{
		ClientID:    client.ID,
		UserID:      -1,
		TokenID:     -1,
		Status:      "pending",
		Expires:     time.Now().UTC().Add(time.Second * deviceCodeTTL),
		LastChecked: time.Now().UTC().Add(-time.Second * deviceCodeInterval),
		Scopes:      f.Scope.Value(),
	}).store(code); err != nil {
		server.Err(w, r, errServerError.withError(err))
		return
	}

	server.Render(w, r, 200, deviceAuthorizationResponse{
		DeviceCode:              deviceCode,
		UserCode:                string(code),
		VerificationURI:         verificationURI.String(),
		VerificationURIComplete: verificationURIComplete.String(),
		ExpiresIn:               deviceCodeTTL,
		Interval:                deviceCodeInterval,
	})
}

// deviceCodeHandler is the handler for the token with
// "urn:ietf:params:oauth:grant-type:device_code" grant_type.
func (api *oauthAPI) deviceCodeHandler(w http.ResponseWriter, r *http.Request) {
	f := getTokenForm(r.Context())
	client, err := loadClient(f.ClientID.Value(), grantTypeDeviceCode)
	if err != nil {
		server.Err(w, r, err)
		return
	}

	code, err := userCodeFromDeviceCode(f.DeviceCode.Value())
	if err != nil {
		server.Err(w, r, errServerError.withError(err))
		return
	}

	req, err := loadDeviceAuthorizationRequest(code)
	if err != nil {
		server.Err(w, r, errServerError.withError(err))
		return
	}

	switch req.Status {
	case "":
		server.Err(w, r, errExpiredToken)
	case codeRequestPending:
		if time.Now().UTC().Sub(req.LastChecked) < time.Second*deviceCodeInterval {
			server.Err(w, r, errSlowDown)
			return
		}

		req.LastChecked = time.Now().UTC()
		defer func() {
			if err := req.store(code); err != nil {
				server.Log(r).Error("store error", slog.Any("err", err))
			}
		}()

		server.Err(w, r, errAuthorizationPending)
	case codeRequestDenied:
		server.Err(w, r, errAccessDenied)
	case codeRequestGranted:
		// Request was granted, create a token
		user, err := users.Users.GetOne(goqu.C("id").Eq(req.UserID))
		if err != nil {
			server.Err(w, r, errServerError.withError(err))
		}

		var t *tokens.Token
		if req.TokenID == -1 {
			// Create a token when it doesn't exist.
			t = &tokens.Token{
				UserID:      &user.ID,
				IsEnabled:   true,
				Application: client.Name,
				Roles:       req.Scopes,
				ClientInfo:  client.toClientInfo(),
			}
			if err = tokens.Tokens.Create(t); err != nil {
				server.Err(w, r,
					errServerError.withDescription("can't create token").withError(err),
				)
				return
			}

			// Store the token ID.
			req.TokenID = t.ID
			if err := req.store(code); err != nil {
				server.Err(w, r, errServerError.withError(err))
				return
			}
		} else {
			// Retrieve the token.
			t, err = tokens.Tokens.GetOne(goqu.C("id").Eq(req.TokenID))
			if err != nil {
				server.Err(w, r, errServerError.withError(err))
				return
			}
		}

		token, err := configs.Keys.TokenKey().Encode(t.UID)
		if err != nil {
			server.Err(w, r,
				errServerError.withDescription("can't encode token").withError(err),
			)
			return
		}
		server.Render(w, r, http.StatusCreated, tokenResponse{
			UID:         t.UID,
			AccessToken: token,
			TokenType:   "Bearer",
			Scope:       strings.Join(t.Roles, " "),
		})
	default:
		server.Err(w, r, errServerError.withError(fmt.Errorf("unknown status: %s", req.Status)))
	}
}
