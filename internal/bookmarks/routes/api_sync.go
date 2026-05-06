// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"net/http"

	"github.com/doug-martin/goqu/v9"

	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/bookmarks/converter"
	"codeberg.org/readeck/readeck/internal/bookmarks/dataset"
	"codeberg.org/readeck/readeck/internal/db"
	"codeberg.org/readeck/readeck/internal/db/exp"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/pkg/forms/v2"
)

func (api *apiRouter) bookmarkSyncList(w http.ResponseWriter, r *http.Request) {
	f := forms.BindAs[syncListForm](r)

	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	ds := bookmarks.Bookmarks.Query().
		Select(
			goqu.C("uid").Table("b"),
			goqu.C("updated").Table("b").As("time"),
			goqu.V("update").As("type"),
		).
		Where(goqu.C("user_id").Table("b").Eq(auth.GetRequestUser(r).ID))

	if !f.Since.Value().IsZero() {
		// When querying with ?since=, we perform a union with bookmark_removed
		// to build some kind of update/delete log.
		since := f.Since.Value().UTC()

		ds = ds.Where(
			exp.DateTime(goqu.C("updated").Table("b")).
				Gte(exp.DateTime(since)),
		)
		ds = ds.Union(
			db.Q().
				From(goqu.T(db.TableBookmarkRemoved).As("r")).
				Select(
					goqu.C("uid").Table("r"),
					goqu.C("deleted").Table("r").As("time"),
					goqu.V("delete").As("type"),
				).
				Where(
					goqu.C("user_id").Table("r").Eq(auth.GetRequestUser(r).ID),
					exp.DateTime(goqu.C("deleted").Table("r")).
						Gte(exp.DateTime(since)),
				),
		)
	}

	ds = ds.Order(goqu.C("time").Desc())

	bl, err := dataset.NewBookmarkSyncList(r.Context(), ds)
	if err != nil {
		server.Err(w, r, err)
		return
	}

	server.WriteEtag(w, r, bl)
	server.WriteLastModified(w, r, bl)
	if !server.HandleCaching(w, r) {
		server.Render(w, r, http.StatusOK, bl)
	}
}

func (api *apiRouter) bookmarkSync(w http.ResponseWriter, r *http.Request) {
	f := newSyncForm(r.Context())
	forms.Bind(r, f)

	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	ds := bookmarks.Bookmarks.Query().
		Where(
			goqu.C("user_id").Table("b").Eq(auth.GetRequestUser(r).ID),
		).
		Order(exp.DateTime(goqu.C("updated")).Asc())

	if order := f.toOrderedExpressions(); order != nil {
		ds = ds.Order(order...)
	}

	ids := f.ID.Value()
	if len(ids) > 0 {
		ds = ds.Where(goqu.C("uid").In(ids))
	}

	seq := dataset.NewBookmarkIterator(r.Context(), ds)
	if err := converter.NewSyncExporter(
		converter.WithSyncJSON(f.WithJSON.Value()),
		converter.WithSyncHTML(f.WithHTML.Value()),
		converter.WithSyncMarkdown(f.WithMarkdown.Value()),
		converter.WithSyncResources(f.WithResources.Value()),
		converter.WithSyncResourcePrefix(f.ResourcePrefix.Value()),
	).IterExport(r.Context(), w, r, seq); err != nil {
		server.Err(w, r, err)
	}
}
