// SPDX-FileCopyrightText: © 2024 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/bookmarks/importer"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/forms"
)

const maxUploadSize = 4 << 20

func (api *apiRouter) bookmarksImport(w http.ResponseWriter, r *http.Request) {
	source := chi.URLParam(r, "source")

	if r.ContentLength > maxUploadSize {
		server.TextMsg(w, r, http.StatusRequestEntityTooLarge, "Payload Too Large")
		return
	}

	adapter := importer.LoadAdapter(source)
	if adapter == nil {
		server.TextMsg(w, r, http.StatusNotFound, fmt.Sprintf(`Import from "%s" does not exist.`, source))
		return
	}

	f := importer.NewImportForm(
		forms.WithTranslator(context.Background(), server.Locale(r)),
		adapter,
	)
	forms.Bind(f, r)

	// If the form is valid, we can load the adapter parameters.
	var data []byte
	var err error
	if f.IsValid() {
		data, err = adapter.Params(f)
	}

	// At this point, any error is fatal.
	if err != nil {
		server.Err(w, r, err)
		return
	}

	// The adapter's form might have changed so we can only check the form's validity now.
	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
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

	w.Header().Add(
		"Location",
		urls.AbsoluteURL(r, "./..", trackID).String(),
	)
	server.TextMsg(w, r, http.StatusAccepted, "import started")
}

func (api *apiRouter) bookmaksImportStatus(w http.ResponseWriter, r *http.Request) {
	trackID := chi.URLParam(r, "trackID")
	progress, err := importer.NewImportProgress(trackID)
	if err != nil {
		server.Err(w, r, err)
		return
	}

	if server.IsTurboRequest(r) {
		server.RenderTurboStreamComponent(w, r, importViews{}.progress(
			trackID,
			importer.ImportBookmarksTask.IsRunning(trackID),
			progress,
		),
			"replace", "import-progress-"+trackID, nil)
		return
	}

	server.Render(w, r, http.StatusOK, map[string]any{
		"scheduled": importer.ImportBookmarksTask.IsRunning(trackID),
		"progress":  progress,
	})
}
