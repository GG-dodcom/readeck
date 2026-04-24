// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/components"
	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/bookmarks/importer"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/forms"
)

func (h *viewsRouter) bookmarksImportMain(w http.ResponseWriter, r *http.Request) {
	tr := server.Locale(r)
	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Bookmarks"), urls.AbsoluteURL(r, "/bookmarks").String()},
		{tr.Gettext("Import")},
	})

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, importViews{}.main())
}

func (h *viewsRouter) bookmarksImportStatus(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "trackID")
	tr := server.Locale(r)
	progress, _ := importer.NewImportProgress(trackID)

	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Bookmarks"), urls.AbsoluteURL(r, "/bookmarks").String()},
		{tr.Gettext("Import"), urls.AbsoluteURL(r, "/bookmarks/import").String()},
		{tr.Gettext("Progress")},
	})

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, importViews{}.status(
		trackID,
		importer.ImportBookmarksTask.IsRunning(trackID),
		progress,
	))
}

func (h *viewsRouter) bookmarksImport(w http.ResponseWriter, r *http.Request) {
	tr := server.Locale(r)
	source := chi.URLParam(r, "source")
	if source == "" {
		server.Status(w, r, http.StatusNotFound)
		return
	}

	adapter := importer.LoadAdapter(source)
	if adapter == nil {
		server.Status(w, r, http.StatusNotFound)
		return
	}

	f := importer.NewImportForm(
		forms.WithTranslator(context.Background(), tr),
		adapter,
	)

	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Bookmarks"), urls.AbsoluteURL(r, "/bookmarks").String()},
		{tr.Gettext("Import"), urls.AbsoluteURL(r, "/bookmarks/import").String()},
		{adapter.Name(tr)},
	})

	if r.Method == http.MethodPost {
		forms.Bind(f, r)

		var data []byte
		var err error
		if f.IsValid() {
			data, err = adapter.Params(f)
		}
		if err != nil {
			server.Err(w, r, err)
			return
		}

		if !f.IsValid() {
			server.RenderComponent(
				w, r.WithContext(ctx),
				http.StatusUnprocessableEntity,
				importViews{}.form(adapter, f),
			)
			return
		}

		ignoreDuplicates := f.Get("ignore_duplicates").(forms.TypedField[bool]).V()

		// Create the import task
		trackID := importer.GetTrackID(server.GetReqID(r))
		err = importer.ImportBookmarksTask.Run(trackID, importer.ImportParams{
			Source:          source,
			Data:            data,
			UserID:          auth.GetRequestUser(r).ID,
			RequestID:       server.GetReqID(r),
			AllowDuplicates: !ignoreDuplicates,
			Label:           f.Get("label").String(),
			Archive:         f.Get("archive").(forms.TypedField[bool]).V(),
			MarkRead:        f.Get("mark_read").(forms.TypedField[bool]).V(),
		})
		if err != nil {
			server.Err(w, r, err)
			return
		}

		server.Redirect(w, r, "./..", trackID)
		return
	}

	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, importViews{}.form(adapter, f))
}
