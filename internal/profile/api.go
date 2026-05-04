// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package profile

import (
	"context"
	"net/http"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/auth/tokens"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/db/exp"
	"codeberg.org/readeck/readeck/internal/db/scanner"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/ctxr"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

type (
	ctxTokenListKey struct{}
	ctxtTokenKey    struct{}
)

var (
	withTokenList, getTokenList = ctxr.WithGetter[*tokenItemList](ctxTokenListKey{})
	withToken, getToken         = ctxr.WithGetter[*tokenItem](ctxtTokenKey{})
)

type tokenType int8

const (
	anyToken tokenType = iota
	userToken
	clientToken
)

// profileAPI is the base settings API router.
type profileAPI struct {
	chi.Router
	srv *server.Server
}

// newProfileAPI returns a SettingAPI with its routes set up.
func newProfileAPI(s *server.Server) *profileAPI {
	r := server.AuthenticatedRouter()
	api := &profileAPI{r, s}

	r.With(server.WithPermission("api:profile", "info")).
		Get("/", api.profileInfo)

	r.With(server.WithPermission("api:profile:tokens", "read"), api.withTokenList(anyToken)).
		Get("/tokens", api.tokenList)

	r.With(server.WithPermission("api:profile", "write")).Group(func(r chi.Router) {
		r.Patch("/", api.profileUpdate)
	})

	r.With(server.WithPermission("api:profile:tokens", "delete")).Group(func(r chi.Router) {
		r.With(api.withToken(anyToken)).Delete("/tokens/{uid}", api.tokenDelete)
	})

	return api
}

// userProfile is the mapping returned by the profileInfo route.
type profileInfoProvider struct {
	Name        string   `json:"name"`
	ID          string   `json:"id"`
	Application string   `json:"application"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
}
type profileInfoUser struct {
	Username string              `json:"username,omitempty"`
	Email    string              `json:"email,omitempty"`
	Created  *time.Time          `json:"created,omitempty"`
	Updated  *time.Time          `json:"updated,omitempty"`
	Settings *users.UserSettings `json:"settings,omitempty"`
}
type profileInfo struct {
	Provider profileInfoProvider `json:"provider"`
	User     profileInfoUser     `json:"user"`
}

// profileInfo returns the current user information.
func (api *profileAPI) profileInfo(w http.ResponseWriter, r *http.Request) {
	info := auth.GetRequestAuthInfo(r)

	res := profileInfo{
		Provider: profileInfoProvider{
			Name:        info.Provider.Name,
			Application: info.Provider.Application,
			ID:          info.Provider.ID,
			Roles:       info.Provider.Roles,
			Permissions: auth.GetPermissions(r),
		},
		User: profileInfoUser{
			Username: info.User.Username,
		},
	}

	if auth.HasPermission(r, "api:profile", "read") {
		res.User.Email = info.User.Email
		res.User.Created = &info.User.Created
		res.User.Updated = &info.User.Updated
		res.User.Settings = info.User.Settings
	}

	if res.Provider.Roles == nil {
		res.Provider.Roles = []string{info.User.Group}
	}

	server.Render(w, r, 200, res)
}

// profileUpdate updates the current user profile information.
func (api *profileAPI) profileUpdate(w http.ResponseWriter, r *http.Request) {
	user := auth.GetRequestUser(r)
	f := forms.BindAs[profileForm](r, withProfileUser(user))

	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	updated, err := f.update()
	if err != nil {
		server.Err(w, r, err)
		return
	}

	server.Render(w, r, 200, updated)
}

func (api *profileAPI) withTokenList(t tokenType) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pf := server.GetPageParams(r, 30)
			if pf == nil {
				server.Status(w, r, http.StatusNotFound)
				return
			}

			ds := tokens.Tokens.Query().
				Where(
					goqu.C("user_id").Eq(auth.GetRequestUser(r).ID),
				).
				Order(
					exp.DateTime(goqu.C("last_used").Table("t")).Desc().NullsLast(),
					exp.DateTime(goqu.C("created").Table("t")).Desc(),
				).
				Limit(uint(pf.Limit.Value())).
				Offset(uint(pf.Offset.Value()))

			switch t {
			case userToken:
				ds = ds.Where(goqu.C("client_info").IsNull())
			case clientToken:
				ds = ds.Where(goqu.C("client_info").IsNotNull())
			}

			var res *tokenItemList
			var err error
			if res, err = newTokenItemList(r.Context(), ds); err != nil {
				server.Err(w, r, err)
				return
			}

			ctx := withTokenList(r.Context(), res)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (api *profileAPI) withToken(t tokenType) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid := chi.URLParam(r, "uid")

			ds := tokens.Tokens.Query().
				Where(
					goqu.C("uid").Table("t").Eq(uid),
					goqu.C("user_id").Eq(auth.GetRequestUser(r).ID),
				)

			switch t {
			case userToken:
				ds = ds.Where(goqu.C("client_info").IsNull())
			case clientToken:
				ds = ds.Where(goqu.C("client_info").IsNotNull())
			}

			t := new(tokens.Token)
			found, err := ds.ScanStruct(t)

			if !found {
				server.Status(w, r, http.StatusNotFound)
				return
			}
			if err != nil {
				server.Err(w, r, err)
				return
			}

			item := newTokenItem(r.Context(), t)
			ctx := withToken(r.Context(), item)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (api *profileAPI) tokenList(w http.ResponseWriter, r *http.Request) {
	tl := getTokenList(r.Context())

	server.SendPaginationHeaders(w, r, tl.Pagination)
	server.Render(w, r, http.StatusOK, tl.Items)
}

func (api *profileAPI) tokenDelete(w http.ResponseWriter, r *http.Request) {
	ti := getToken(r.Context())
	if err := ti.Delete(); err != nil {
		server.Err(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type tokenItemList struct {
	Count      int64
	Pagination server.Pagination
	Items      []*tokenItem
}

type tokenItem struct {
	*tokens.Token `json:"-"`

	ID         string     `json:"id"`
	Href       string     `json:"href"`
	Created    time.Time  `json:"created"`
	LastUsed   *time.Time `json:"last_used"`
	Expires    *time.Time `json:"expires"`
	IsEnabled  bool       `json:"is_enabled"`
	IsDeleted  bool       `json:"is_deleted"`
	Roles      []string   `json:"roles"`
	RoleNames  []string   `json:"-"`
	ClientName string     `json:"client_name"`
	ClientURI  string     `json:"client_uri"`
	ClientLogo string     `json:"client_logo"`
}

func newTokenItem(ctx context.Context, t *tokens.Token) *tokenItem {
	tr := server.LocaleContext(ctx)
	res := &tokenItem{
		Token:     t,
		ID:        t.UID,
		Href:      urls.AbsoluteURLContext(ctx, ".", t.UID).String(),
		Created:   t.Created,
		LastUsed:  t.LastUsed,
		Expires:   t.Expires,
		IsEnabled: t.IsEnabled,
		IsDeleted: deleteTokenTask.IsRunning(t.ID),
		Roles:     t.Roles,
		RoleNames: users.GroupNames(tr, t.Roles),
	}

	if t.ClientInfo != nil && t.ClientInfo.ID != "" {
		res.ClientName = t.ClientInfo.Name
		res.ClientURI = t.ClientInfo.Website
		res.ClientLogo = t.ClientInfo.Logo
	}

	return res
}

func newTokenItemList(ctx context.Context, ds *goqu.SelectDataset) (*tokenItemList, error) {
	res := &tokenItemList{
		Items: []*tokenItem{},
	}

	var err error
	if res.Count, err = ds.ClearOrder().ClearLimit().ClearOffset().Count(); err != nil {
		return nil, err
	}

	if limit, ok := ds.GetClauses().Limit().(uint); ok {
		res.Pagination = server.NewPagination(ctx,
			int(res.Count), int(limit), int(ds.GetClauses().Offset()),
		)
	}

	if res.Count == 0 {
		return res, nil
	}

	for item, err := range scanner.IterTransform(ctx, ds, newTokenItem) {
		if err != nil {
			return nil, err
		}
		res.Items = append(res.Items, item)
	}
	return res, nil
}
