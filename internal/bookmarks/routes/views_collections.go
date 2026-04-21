// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"log/slog"
	"net/http"

	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/bookmarks/dataset"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/pkg/forms"
)

func (h *viewsRouter) collectionList(w http.ResponseWriter, r *http.Request) {
	server.RenderComponent(
		w, r, http.StatusOK,
		Views{}.collectionList(getCollectionList(r.Context())),
	)
}

func (h *viewsRouter) collectionCreate(w http.ResponseWriter, r *http.Request) {
	f := newCollectionForm(server.Locale(r), r)

	switch r.Method {
	case http.MethodGet:
		// Add values from query string but don't perform validation
		forms.BindURL(f, r)
	case http.MethodPost:
		forms.Bind(f, r)
		if f.IsValid() {
			c, err := f.createCollection(auth.GetRequestUser(r).ID)
			if err != nil {
				server.Log(r).Error("", slog.Any("err", err))
			} else {
				server.Redirect(w, r, "./..", c.UID)
				return
			}
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
	}

	bl := getBookmarkList(r.Context())

	tc := getBaseContext(r.Context())
	tc["Pagination"] = bl.Pagination
	tc["Bookmarks"] = bl.Items
	tc["Form"] = f

	server.RenderTemplate(w, r, 200, "/bookmarks/collection_create", tc)
}

func (h *viewsRouter) collectionInfo(w http.ResponseWriter, r *http.Request) {
	c := getCollection(r.Context())
	item := dataset.NewCollection(r.Context(), c)

	f := newCollectionForm(server.Locale(r), r)
	f.setCollection(c)

	if r.Method == http.MethodPost {
		forms.Bind(f, r)
		if f.IsValid() {
			if _, err := f.updateCollection(c); err != nil {
				server.Log(r).Error("", slog.Any("err", err))
			} else {
				tr := server.Locale(r)
				server.AddFlash(w, r, "success", tr.Gettext("Collection updated."))
				server.Redirect(w, r, c.UID+"?edit=1")
				return
			}
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
	}

	bl := getBookmarkList(r.Context())

	tc := getBaseContext(r.Context())
	tc["Editing"] = r.URL.Query().Get("edit") == "1"
	tc["Item"] = item
	tc["Form"] = f
	tc["Pagination"] = bl.Pagination
	tc["Bookmarks"] = bl.Items

	server.RenderTemplate(w, r, 200, "/bookmarks/collection", tc)
}

func (h *viewsRouter) collectionDelete(w http.ResponseWriter, r *http.Request) {
	f := newCollectionDeleteForm(server.Locale(r))
	f.Get("_to").Set("/bookmarks/collections")
	forms.Bind(f, r)

	c := getCollection(r.Context())

	// This update forces cache invalidation
	if err := c.Update(map[string]any{}); err != nil {
		server.Err(w, r, err)
		return
	}
	if err := f.trigger(c); err != nil {
		server.Err(w, r, err)
		return
	}
	server.Redirect(w, r, f.Get("_to").String())
}
