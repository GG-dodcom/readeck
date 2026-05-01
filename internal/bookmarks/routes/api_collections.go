// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"errors"
	"net/http"

	"github.com/doug-martin/goqu/v9"
	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/bookmarks/dataset"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/forms"
)

func (api *apiRouter) collectionList(w http.ResponseWriter, r *http.Request) {
	cl := getCollectionList(r.Context())
	server.SendPaginationHeaders(w, r, cl.Pagination)
	server.Render(w, r, http.StatusOK, cl.Items)
}

func (api *apiRouter) collectionInfo(w http.ResponseWriter, r *http.Request) {
	c := getCollection(r.Context())
	item := dataset.NewCollection(r.Context(), c)
	server.Render(w, r, http.StatusOK, item)
}

func (api *apiRouter) collectionCreate(w http.ResponseWriter, r *http.Request) {
	f := newCollectionForm(server.Locale(r), r)

	forms.Bind(f, r)
	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	c, err := f.createCollection(auth.GetRequestUser(r).ID)
	if err != nil {
		server.Err(w, r, err)
		return
	}

	w.Header().Set("Location", urls.AbsoluteURL(r, ".", c.UID).String())
	server.TextMsg(w, r, http.StatusCreated, "Collection created")
}

func (api *apiRouter) collectionUpdate(w http.ResponseWriter, r *http.Request) {
	c := getCollection(r.Context())

	f := newCollectionForm(server.Locale(r), r)
	f.setCollection(c)
	forms.Bind(f, r)

	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	updated, err := f.updateCollection(c)
	if err != nil {
		server.Err(w, r, err)
		return
	}

	server.Render(w, r, http.StatusOK, updated)
}

func (api *apiRouter) collectionDelete(w http.ResponseWriter, r *http.Request) {
	c := getCollection(r.Context())
	if err := c.Delete(); err != nil {
		server.Err(w, r, err)
		return
	}

	server.Status(w, r, http.StatusNoContent)
}

func (api *apiRouter) withColletionList(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pf := server.GetPageParams(r, 30)
		if pf == nil {
			server.Status(w, r, http.StatusNotFound)
			return
		}

		ds := bookmarks.Collections.Query().
			Select(
				"c.id", "c.uid", "c.user_id", "c.created", "c.updated",
				"c.name", "c.is_pinned", "c.filters",
			).
			Where(
				goqu.C("user_id").Table("c").Eq(auth.GetRequestUser(r).ID),
			)

		ds = ds.Order(goqu.I("name").Asc()).
			Limit(uint(pf.Limit.Value())).
			Offset(uint(pf.Offset.Value()))

		res, err := dataset.NewCollectionList(r.Context(), ds)
		if err != nil {
			if errors.Is(err, bookmarks.ErrCollectionNotFound) {
				server.TextMsg(w, r, http.StatusNotFound, "not found")
			} else {
				server.Err(w, r, err)
			}
			return
		}

		ctx := withCollectionList(r.Context(), res)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (api *apiRouter) withCollection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := chi.URLParam(r, "uid")

		c, err := bookmarks.Collections.GetOne(
			goqu.C("uid").Eq(uid),
			goqu.C("user_id").Eq(auth.GetRequestUser(r).ID),
		)
		if err != nil {
			server.Status(w, r, http.StatusNotFound)
			return
		}

		ctx := withCollection(r.Context(), c)
		ctx = withBookmarkListTaggers(ctx, []server.Etagger{c})

		if _, ok := checkBookmarkOrder(ctx); !ok {
			ctx = withBookmarkOrder(ctx, orderExpressionList{goqu.T("b").Col("created").Desc()})
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
