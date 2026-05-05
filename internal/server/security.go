// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/ctxr"
	"codeberg.org/readeck/readeck/pkg/http/csp"
	"codeberg.org/readeck/readeck/pkg/http/permissionspolicy"
)

type (
	ctxCSPNonceKey     struct{}
	ctxCSPKey          struct{}
	ctxUnauthorizedKey struct{}
)

// Context setters.
// nolint:revive
var (
	withCSPNonce, GetCSPNonce         = ctxr.WithChecker[string](ctxCSPNonceKey{})
	withCSP, getCSP                   = ctxr.WithChecker[csp.Policy](ctxCSPKey{})
	withUnauthorized, getUnauthorized = ctxr.WithChecker[int](ctxUnauthorizedKey{})
)

const (
	unauthorizedDefault = iota
	unauthorizedRedir
)

type cspReport struct {
	Report map[string]any `json:"csp-report"`
}

// getDefaultCSP returns the default Content Security Policy
// There are no definition on script-src and style-src because
// the SetSecurityHeaders middleware will set a nonce value
// for each of them.
func getDefaultCSP() csp.Policy {
	return csp.Policy{
		"base-uri":        {csp.None},
		"default-src":     {csp.Self},
		"font-src":        {csp.Self},
		"form-action":     {csp.Self},
		"frame-ancestors": {csp.None},
		"img-src":         {csp.Self, csp.Data},
		"media-src":       {csp.Self, csp.Data},
		"object-src":      {csp.None},
		"script-src":      {csp.ReportSample},
		"style-src":       {csp.ReportSample},
	}
}

// GetCSPHeader extracts the current CSPHeader from the request's context.
func GetCSPHeader(r *http.Request) csp.Policy {
	if c, ok := getCSP(r.Context()); ok {
		return c
	}
	return getDefaultCSP()
}

// SetSecurityHeaders adds some headers to improve client side security.
func SetSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var nonce string
		if nonce = r.Header.Get("x-turbo-nonce"); nonce == "" {
			nonce = csp.MakeNonce()
		}

		policy := getDefaultCSP()
		policy.Add("script-src", fmt.Sprintf("'nonce-%s'", nonce), csp.UnsafeInline)
		policy.Add("style-src", fmt.Sprintf("'nonce-%s'", nonce), csp.UnsafeInline)
		policy.Add("report-uri", urls.AbsoluteURL(r, "/logger/csp-report").String())

		policy.Write(w.Header())
		permissionspolicy.DefaultPolicy.Write(w.Header())
		w.Header().Set("Referrer-Policy", "same-origin, strict-origin")
		w.Header().Add("X-Frame-Options", "DENY")
		w.Header().Add("X-Content-Type-Options", "nosniff")
		w.Header().Add("X-XSS-Protection", "1; mode=block")
		w.Header().Add("X-Robots-Tag", "noindex, nofollow, noarchive")

		ctx := withCSPNonce(r.Context(), nonce)
		ctx = withCSP(ctx, policy)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func cspReportHandler(w http.ResponseWriter, r *http.Request) {
	report := cspReport{}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&report); err != nil {
		Log(r).Error("server error", slog.Any("err", err))
		return
	}

	attrs := []slog.Attr{}
	for k, v := range report.Report {
		attrs = append(attrs, slog.Any(k, v))
	}
	Log(r).WithGroup("report").LogAttrs(
		context.Background(),
		slog.LevelWarn,
		"CSP violation",
		attrs...,
	)

	w.WriteHeader(http.StatusNoContent)
}

// unauthorizedHandler is a handler used by the session authentication provider.
// It sends different responses based on the context.
func unauthorizedHandler(w http.ResponseWriter, r *http.Request) {
	unauthorizedCtx, _ := getUnauthorized(r.Context())

	switch unauthorizedCtx {
	case unauthorizedDefault:
		w.Header().Add("WWW-Authenticate", `Basic realm="Readeck Authentication"`)
		w.Header().Add("WWW-Authenticate", `Bearer realm="Bearer token"`)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "Unauthorized")
	case unauthorizedRedir:
		if !configs.Config.Commissioned {
			Redirect(w, r, "/onboarding")
			return
		}

		redir := urls.AbsoluteURL(r, "/login")

		// Add the current path as a redirect query parameter
		// to the login route.
		if r.Method == http.MethodGet {
			q := redir.Query()
			q.Add("r", urls.CurrentPath(r))
			redir.RawQuery = q.Encode()
		}

		w.Header().Set("Location", redir.String())
		w.WriteHeader(http.StatusSeeOther)
	}
}

// WithRedirectLogin sets the unauthorized handler to redirect to the login page.
func WithRedirectLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withUnauthorized(r.Context(), unauthorizedRedir)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
