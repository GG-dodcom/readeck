// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package oauth2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"

	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/pkg/forms"
)

var (
	errAccessDenied          = oauthError{name: "access_denied"}
	errAuthorizationPending  = oauthError{name: "authorization_pending"}
	errExpiredToken          = oauthError{name: "expired_token"}
	errInvalidClient         = oauthError{name: "invalid_client", status: http.StatusUnauthorized}
	errInvalidClientMetadata = oauthError{name: "invalid_client_metadata"}
	errInvalidGrant          = oauthError{name: "invalid_grant"}
	errInvalidRedirectURI    = oauthError{name: "invalid_redirect_uri"}
	errInvalidRequest        = oauthError{name: "invalid_request"}
	errInvalidScope          = oauthError{name: "invalid_scope"}
	errServerError           = oauthError{name: "server_error", status: http.StatusInternalServerError}
	errSlowDown              = oauthError{name: "slow_down"}
	errUnauthorizedClient    = oauthError{name: "unauthorized_client"}
)

// oauthError describes an OAuth error as specified in
// https://datatracker.ietf.org/doc/html/rfc6749#section-4.2.2.1
// https://datatracker.ietf.org/doc/html/rfc6749#section-5.2
type oauthError struct {
	status      int
	name        string
	description string
	error       error
}

// withDescription returns a copy of the error with a description that
// reflect in "error_description" when rendered.
func (e oauthError) withDescription(description string) oauthError {
	ne := e
	ne.description = description
	return ne
}

// withError returns a copy of the error with an [error]
// that will be logged when needed.
func (e oauthError) withError(err error) oauthError {
	ne := e
	ne.error = err
	return ne
}

// Error implement [error].
func (e oauthError) Error() string {
	res := e.name
	if e.description != "" {
		res += " (" + e.description + ")"
	}
	return res
}

// Log produces a log when called from [server.Err].
func (e oauthError) Log(log *slog.Logger) {
	level := slog.LevelWarn
	if e.status >= 500 {
		level = slog.LevelError
	}

	attrs := []slog.Attr{slog.String("name", e.name)}
	if e.error != nil {
		attrs = append(attrs, slog.Any("err", e.error))
	}

	log.LogAttrs(context.Background(), level, "oauth", attrs...)
}

// StatusCode returns the HTTP status used by [server.Err].
func (e oauthError) StatusCode() int {
	if e.status == 0 {
		return 400
	}
	return e.status
}

// MarshalJSON implements [json.Marshaler].
func (e oauthError) MarshalJSON() ([]byte, error) {
	res := map[string]string{
		"error": e.name,
	}
	if e.description != "" {
		res["error_description"] = e.description
	}

	return json.Marshal(res)
}

// Is compares two errors. Only [oauthError] is considered and they
// are compared using their names.
func (e oauthError) Is(err error) bool {
	switch t := err.(type) {
	case oauthError:
		return e.name == t.name
	default:
		return false
	}
}

// redirect returns the given [*url.URL] with the error redirection parameters.
func (e oauthError) redirect(w http.ResponseWriter, r *http.Request, u *url.URL, params url.Values) {
	e.Log(server.Log(r))

	res := new(url.URL)
	*res = *u

	if params == nil {
		params = url.Values{}
	}

	params.Set("error", e.name)
	if e.description != "" {
		params.Set("error_description", e.description)
	}

	res.RawQuery = params.Encode()

	w.Header().Set("Location", res.String())
	w.WriteHeader(http.StatusFound)
}

// newFormError converts an invalid form to an [oauthError].
func newFormError(f forms.FormBinder) oauthError {
	for _, err := range f.Errors() {
		return errInvalidRequest.withDescription(err.Error())
	}

	for _, name := range slices.Sorted(maps.Keys(f.Fields())) {
		field := f.Fields()[name]
		if len(field.Errors()) > 0 {
			return errInvalidRequest.withDescription(
				fmt.Sprintf(`error on field "%s": %s`, name, field.Errors()),
			)
		}
	}

	return errServerError.withError(errors.New("no form error"))
}
