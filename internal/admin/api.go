// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package admin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/db/scanner"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/ctxr"
	"codeberg.org/readeck/readeck/pkg/forms"
)

type (
	ctxUserListKey struct{}
	ctxUserKey     struct{}
)

var (
	withUserList, getUserList = ctxr.WithGetter[*userList](ctxUserListKey{})
	withUser, getUser         = ctxr.WithGetter[*users.User](ctxUserKey{})
)

var errSameUser = errors.New("same user as authenticated")

type adminAPI struct {
	chi.Router
	srv *server.Server
}

func newAdminAPI(s *server.Server) *adminAPI {
	r := server.AuthenticatedRouter()
	api := &adminAPI{r, s}

	r.With(server.WithPermission("api:admin:users", "read")).Group(func(r chi.Router) {
		r.With(api.withUserList).Get("/users", api.userList)
		r.With(api.withUser).Get("/users/{uid:[a-zA-Z0-9]{18,22}}", api.userInfo)
	})

	r.With(server.WithPermission("api:admin:users", "write")).Group(func(r chi.Router) {
		r.With(api.withUserList).Post("/users", api.userCreate)
		r.With(api.withUser).Patch("/users/{uid:[a-zA-Z0-9]{18,22}}", api.userUpdate)
		r.With(api.withUser).Delete("/users/{uid:[a-zA-Z0-9]{18,22}}", api.userDelete)
	})

	return api
}

func (api *adminAPI) withUserList(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pf := server.GetPageParams(r, 50)
		if pf == nil {
			server.Status(w, r, http.StatusNotFound)
			return
		}

		ds := users.Users.Query().
			Order(goqu.I("username").Asc()).
			Limit(uint(pf.Limit.Value())).
			Offset(uint(pf.Offset.Value()))

		res, err := newUserList(r.Context(), ds)
		if err != nil {
			server.Err(w, r, err)
			return
		}

		ctx := withUserList(r.Context(), res)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (api *adminAPI) withUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userid := chi.URLParam(r, "uid")

		u, err := users.Users.GetOne(
			goqu.C("uid").Eq(userid),
		)
		if err != nil {
			server.Status(w, r, http.StatusNotFound)
			return
		}

		ctx := withUser(r.Context(), u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (api *adminAPI) deleteUser(r *http.Request, u *users.User) error {
	if u.ID == auth.GetRequestUser(r).ID {
		return errSameUser
	}

	// Remove user's bookmarks first
	if err := bookmarks.Bookmarks.DeleteUserBookmakrs(u); err != nil {
		return err
	}

	return u.Delete()
}

func (api *adminAPI) userList(w http.ResponseWriter, r *http.Request) {
	ul := getUserList(r.Context())

	server.SendPaginationHeaders(w, r, ul.Pagination)
	server.Render(w, r, http.StatusOK, ul.Items)
}

func (api *adminAPI) userInfo(w http.ResponseWriter, r *http.Request) {
	u := getUser(r.Context())
	item, err := newExtendedUserItem(r.Context(), u)
	if err != nil {
		server.Err(w, r, err)
		return
	}
	item.Settings = u.Settings

	server.Render(w, r, http.StatusOK, item)
}

func (api *adminAPI) userCreate(w http.ResponseWriter, r *http.Request) {
	f := forms.BindAs[userForm](r)
	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	u, err := f.createUser()
	if err != nil {
		server.Err(w, r, err)
		return
	}

	w.Header().Set("Location", urls.AbsoluteURL(r, ".", u.UID).String())
	w.Header().Set("User-Id", u.UID)
	server.TextMsg(w, r, http.StatusCreated, "User created")
}

func (api *adminAPI) userUpdate(w http.ResponseWriter, r *http.Request) {
	u := getUser(r.Context())

	f := forms.New[userForm](r.Context())
	f.SetUser(u)

	forms.Bind(r, f)
	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	updated, err := f.updateUser(u)
	if err != nil {
		server.Err(w, r, err)
		return
	}
	server.Render(w, r, http.StatusOK, updated)
}

func (api *adminAPI) userDelete(w http.ResponseWriter, r *http.Request) {
	u := getUser(r.Context())

	err := api.deleteUser(r, u)
	if err == nil {
		server.Status(w, r, http.StatusNoContent)
		return
	}
	if errors.Is(err, errSameUser) {
		server.TextMsg(w, r, http.StatusConflict, err.Error())
		return
	}

	server.Err(w, r, err)
}

type userList struct {
	Count      int64
	Pagination server.Pagination
	Items      []*userItem
}

func newUserList(ctx context.Context, ds *goqu.SelectDataset) (*userList, error) {
	res := &userList{
		Items: []*userItem{},
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

	for item, err := range scanner.IterTransform(ctx, ds, newUserItem) {
		if err != nil {
			return nil, err
		}
		res.Items = append(res.Items, item)
	}

	return res, nil
}

type userItem struct {
	ID        string              `json:"id"`
	Href      string              `json:"href"`
	Username  string              `json:"username"`
	Email     string              `json:"email"`
	Group     string              `json:"group"`
	Created   time.Time           `json:"created"`
	Updated   time.Time           `json:"updated"`
	LastLogin time.Time           `json:"last_login"`
	Settings  *users.UserSettings `json:"settings,omitempty"`
	IsDeleted bool                `json:"is_deleted"`
}

func newUserItem(ctx context.Context, u *users.User) *userItem {
	return &userItem{
		ID:        u.UID,
		Href:      urls.AbsoluteURLContext(ctx, "/api/admin/users", u.UID).String(),
		Username:  u.Username,
		Email:     u.Email,
		Group:     u.Group,
		Created:   u.Created,
		Updated:   u.Updated,
		LastLogin: u.LastLogin,
		IsDeleted: deleteUserTask.IsRunning(u.ID),
	}
}

type extendedUserItem struct {
	*userItem

	LastActivity      time.Time `json:"last_activity"`
	BookmarkCount     int       `json:"bookmark_count"`
	BookmarkDiskUsage uint64    `json:"bookmark_disk_usage"`
}

func newExtendedUserItem(ctx context.Context, u *users.User) (*extendedUserItem, error) {
	item := &extendedUserItem{
		userItem: newUserItem(ctx, u),
	}
	var err error

	item.LastActivity, err = u.LastActivity()
	if err != nil {
		return nil, err
	}

	item.BookmarkCount, item.BookmarkDiskUsage, err = bookmarks.Bookmarks.UserDiskUsage(u)
	if err != nil {
		return nil, err
	}

	return item, nil
}

func deleteUser(u *users.User) error {
	// Remove user's bookmarks first
	if err := bookmarks.Bookmarks.DeleteUserBookmakrs(u); err != nil {
		return err
	}

	return u.Delete()
}
