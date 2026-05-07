// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/acls"
	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/auth/tokens"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/sessions"
	"codeberg.org/readeck/readeck/pkg/ctxr"
	"codeberg.org/readeck/readeck/pkg/forms"
	"codeberg.org/readeck/readeck/pkg/http/request"
)

type ctxTokenKey struct{}

var withToken, getToken = ctxr.WithGetter[string](ctxTokenKey{})

// Interface guards.
var (
	_ auth.Provider                  = (*TokenAuthProvider)(nil)
	_ auth.LoggerProvider            = (*TokenAuthProvider)(nil)
	_ auth.FeatureCsrfProvider       = (*TokenAuthProvider)(nil)
	_ auth.FeaturePermissionProvider = (*TokenAuthProvider)(nil)

	_ auth.Provider       = (*SessionAuthProvider)(nil)
	_ auth.LoggerProvider = (*SessionAuthProvider)(nil)

	_ auth.Provider       = (*ForwardedAuthProvider)(nil)
	_ auth.LoggerProvider = (*ForwardedAuthProvider)(nil)
)

// TokenAuthProvider handles authentication using a bearer token
// passed in the request "Authorization" header with the scheme
// "Bearer".
type TokenAuthProvider struct{}

// Log implements [auth.LoggerProvider].
func (p *TokenAuthProvider) Log(r *http.Request) *slog.Logger {
	return slog.With(slog.String("@id", request.GetReqID(r.Context())))
}

// Handler sets [tokenAuthProvider] when the client submit an Authorization header
// that contains a token.
func (p *TokenAuthProvider) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if token, ok := p.getToken(r); ok {
			ctx = auth.WithProvider(ctx, p)
			ctx = withToken(ctx, token)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Authenticate performs the authentication using the "Authorization: Bearer"
// header provided.
func (p *TokenAuthProvider) Authenticate(w http.ResponseWriter, r *http.Request) (*http.Request, error) {
	token := getToken(r.Context())
	if token == "" {
		p.denyAccess(w)
		return r, errors.New("invalid authentication header")
	}

	uid, err := configs.Keys.TokenKey().Decode(token)
	if err != nil {
		p.denyAccess(w)
		return r, err
	}

	res, err := tokens.Tokens.GetUser(uid)
	if err != nil {
		p.denyAccess(w)
		return r, err
	}

	if err := res.Token.Update(goqu.Record{
		"last_used": time.Now().UTC(),
	}); err != nil {
		return r, err
	}

	if res.Token.IsExpired() {
		p.denyAccess(w)
		return r, errors.New("expired token")
	}

	ctx := auth.WithAuthInfo(r.Context(), &auth.Info{
		Provider: &auth.ProviderInfo{
			Name:        "bearer token",
			Application: res.Token.Application,
			Roles:       res.Token.Roles,
			ID:          res.Token.UID,
		},
		User: res.User,
	})
	return r.WithContext(ctx), nil
}

// HasPermission checks the permission on the current authentication provider role
// list. If the role list is empty, the user permissions apply.
func (p *TokenAuthProvider) HasPermission(r *http.Request, obj, act string) bool {
	if len(auth.GetRequestAuthInfo(r).Provider.Roles) == 0 {
		return true
	}

	for _, scope := range auth.GetRequestAuthInfo(r).Provider.Roles {
		if acls.Enforce(scope, obj, act) {
			return true
		}
	}

	return false
}

// GetPermissions returns all the permissions attached to the current authentication provider
// role list. If no role is defined, it will fallback to the user permission list.
func (p *TokenAuthProvider) GetPermissions(r *http.Request) []string {
	if len(auth.GetRequestAuthInfo(r).Provider.Roles) == 0 {
		return nil
	}

	return acls.GetPermissions(auth.GetRequestAuthInfo(r).Provider.Roles...)
}

// CsrfExempt is always true for this provider.
func (p *TokenAuthProvider) CsrfExempt(_ *http.Request) bool {
	return true
}

// getToken reads the token from the "Authorization" header.
func (p *TokenAuthProvider) getToken(r *http.Request) (string, bool) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", false
	}

	// Try basic auth first
	if _, token, ok := r.BasicAuth(); ok {
		return token, ok
	}

	// Bearer token otherwise
	const prefix = "Bearer "
	if len(auth) < len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return "", false
	}
	return auth[len(prefix):], true
}

func (p *TokenAuthProvider) denyAccess(w http.ResponseWriter) {
	w.Header().Add("WWW-Authenticate", `Bearer realm="Bearer token"`)
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(http.StatusText(http.StatusUnauthorized)))
}

// SessionAuthProvider is the last authentication provider.
// It's alway enabled in case of every previous provider failing.
type SessionAuthProvider struct{}

// Log implements [auth.LoggerProvider].
func (p *SessionAuthProvider) Log(r *http.Request) *slog.Logger {
	return slog.With(slog.String("@id", request.GetReqID(r.Context())))
}

// Handler always set this provider to the request and it must come as the last one.
// If authentication fails, it will redirect to the login page.
func (p *SessionAuthProvider) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithProvider(r.Context(), p)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Authenticate checks if the request's session cookie is valid and
// the user exists.
func (p *SessionAuthProvider) Authenticate(w http.ResponseWriter, r *http.Request) (*http.Request, error) {
	sess := GetSession(r)
	u, err := p.checkSession(sess)
	if u == nil || err != nil {
		p.clearSession(sess, w, r)
		return r, err
	}

	if sess.Payload.External && !configs.Config.Auth.Forwarded.Enabled {
		p.clearSession(sess, w, r)
		return r, nil
	}

	// lock user when external
	u.Lock(sess.Payload.External)

	// At this point, the user is granted access.
	// We renew its session for another max age duration.
	sess.Save(w, r)
	ctx := auth.WithAuthInfo(r.Context(), &auth.Info{
		Provider: &auth.ProviderInfo{
			Name: "http session",
		},
		User: u,
	})
	return r.WithContext(ctx), nil
}

func (p *SessionAuthProvider) checkSession(sess *sessions.Session) (u *users.User, err error) {
	if sess.IsNew {
		return
	}

	if sess.Payload.User == 0 {
		return
	}

	if sess.Payload.RequiresMFA {
		return
	}

	if u, err = users.Users.GetOne(goqu.C("id").Eq(sess.Payload.User)); err != nil {
		return nil, err
	}

	if u.Seed != sess.Payload.Seed {
		return nil, nil
	}

	return
}

func (p *SessionAuthProvider) clearSession(sess *sessions.Session, w http.ResponseWriter, r *http.Request) {
	sess.Clear(w, r)
	unauthorizedHandler(w, r)
}

// ForwardedAuthProvider handles forwarded authentication provided by a reverse
// proxy through headers.
type ForwardedAuthProvider struct{}

// Log implements [auth.LoggerProvider].
func (p *ForwardedAuthProvider) Log(r *http.Request) *slog.Logger {
	return slog.With(slog.String("@id", request.GetReqID(r.Context())))
}

// Handler accepts requests with forwarded authentication headers.
// When successful, it adds a new session cookie to the request, which
// can then be handled by [SessionAuthProvider].
func (p *ForwardedAuthProvider) Handler(next http.Handler) http.Handler {
	if !p.enabled() {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username := r.Header.Get(configs.Config.Auth.Forwarded.HeaderUser)
		email := r.Header.Get(configs.Config.Auth.Forwarded.HeaderEmail)
		group := r.Header.Get(configs.Config.Auth.Forwarded.HeaderGroup)

		if username == "" || email == "" {
			// No header, stop and jump to next handler.
			next.ServeHTTP(w, r)
			return
		}

		// Check request
		if err := p.checkRequest(r); err != nil {
			Status(w, r, http.StatusForbidden)
			p.Log(r).Error("forwarded auth", slog.Any("err", err))
			return
		}

		// Load user
		f := forms.New[users.ProvisioningForm](
			forms.WithTranslator(r.Context(), Locale(r)),
		)

		user, update, err := f.LoadUser(username, email, group)
		if err != nil {
			if fe, ok := errors.AsType[forms.Errors](err); ok {
				Status(w, r, http.StatusForbidden)
				p.Log(r).Error("forwarded auth", slog.Any("err", fe))
			} else {
				Err(w, r, err)
			}

			return
		}

		if user.IsAnonymous() {
			// If user does not exist we might provision it or reject the request,
			// according to the setting [ForwardedAuthProvider.AllowProvisioning].
			if !p.provisioningEnabled() {
				Status(w, r, http.StatusForbidden)
				p.Log(r).Error("forwarded auth",
					slog.Any("err", "user provisioning not allowed"),
				)
				return
			}
			if err = users.Users.Create(user); err != nil {
				Err(w, r, err)
				return
			}
		} else if len(update) > 0 {
			// When user is known, we might need to update them.
			update["updated"] = time.Now().UTC()
		}

		// Load any existing session
		sess, _ := sessions.New(SessionHandler(), r)
		if !sess.IsNew && sess.Payload.User == user.ID {
			// The session exists and is for the same user, we can stop here.
			if err = user.Update(update); err != nil {
				Err(w, r, err)
				return
			}

			next.ServeHTTP(w, r)
			return
		}

		// Update user with provided update info and last_login
		update["last_login"] = time.Now().UTC()
		if err = user.Update(update); err != nil {
			Err(w, r, err)
			return
		}

		// Encode and add the session cookie to the request
		sess.Payload.User = user.ID
		sess.Payload.Seed = user.Seed
		sess.Payload.External = true
		sess.Payload.RequiresMFA = user.RequiresMFA()

		encoded, err := SessionHandler().Encode(sess.Payload)
		if err != nil {
			Err(w, r, err)
			return
		}

		// Keep existing cookies except the session
		cookies, _ := http.ParseCookie(r.Header.Get("cookie"))
		r.Header.Del("cookie")
		for _, c := range cookies {
			if c.Name != configs.Config.Server.Session.CookieName {
				r.AddCookie(c)
			}
		}

		// Add the new session cookie
		r.AddCookie(&http.Cookie{
			Name:  configs.Config.Server.Session.CookieName,
			Value: base64.URLEncoding.EncodeToString(encoded),
		})

		next.ServeHTTP(w, r)
	})
}

// Authenticate is a noop.
func (p *ForwardedAuthProvider) Authenticate(_ http.ResponseWriter, r *http.Request) (*http.Request, error) {
	return r, nil
}

func (p *ForwardedAuthProvider) checkRequest(r *http.Request) error {
	// Check remote IP address
	remoteIP := request.GetRemoteIP(r.Context())
	if n := p.trustedOrigin(); remoteIP != nil && len(n) > 0 && !slices.ContainsFunc(n, func(cidr *net.IPNet) bool {
		return cidr.Contains(remoteIP)
	}) {
		return fmt.Errorf("unauthorized client (%s)", remoteIP)
	}

	// Check client certificate
	if p.mtlsRequired() && (r.TLS == nil || len(r.TLS.PeerCertificates) == 0) {
		return errors.New("no client certificate provided")
	}

	return nil
}

func (p *ForwardedAuthProvider) enabled() bool {
	return configs.Config.Auth.Forwarded.Enabled
}

func (p *ForwardedAuthProvider) mtlsRequired() bool {
	return configs.Config.Server.ClientCAFile != ""
}

func (p *ForwardedAuthProvider) provisioningEnabled() bool {
	return configs.Config.Auth.Forwarded.ProvisioningEnabled
}

func (p *ForwardedAuthProvider) trustedOrigin() []*net.IPNet {
	return configs.TrustedProxies()
}
