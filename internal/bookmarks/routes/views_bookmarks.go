// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/bookmarks/dataset"
	"codeberg.org/readeck/readeck/internal/bookmarks/tasks"
	"codeberg.org/readeck/readeck/internal/db"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/forms"
	"codeberg.org/readeck/readeck/pkg/http/csp"
	"codeberg.org/readeck/readeck/pkg/utils"
)

const listDefaultLimit = 36

func (h *viewsRouter) withBaseContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count, err := bookmarks.Bookmarks.CountAll(auth.GetRequestUser(r))
		if err != nil {
			server.Err(w, r, err)
			return
		}

		c := server.TC{
			"Count": count,
		}

		ctx := withBaseContext(r.Context(), c)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *viewsRouter) bookmarkList(w http.ResponseWriter, r *http.Request) {
	f := newCreateForm(r)
	tc := getBaseContext(r.Context())
	tc["MaybeSearch"] = false

	switch r.Method {
	case http.MethodGet:
		// prepopulate the URL, no validation takes place at this point
		f.Get("url").Set(r.URL.Query().Get("url"))
	case http.MethodPost:
		// POST => create a new bookmark
		forms.Bind(f, r)
		if f.IsValid() {
			f.Get("created").Set(nil)
			f.Get("feature_find_main").Set(nil)
			f.Get("resource").Set(nil)

			if b, err := f.createBookmark(); err != nil {
				server.Log(r).Error("", slog.Any("err", err))
			} else {
				redir := []string{"/bookmarks"}
				if server.IsTurboRequest(r) {
					redir = append(redir, "unread")
				} else {
					redir = append(redir, b.UID)
				}
				server.Redirect(w, r, redir...)
				return
			}
		}

		// If the URL is not valid, set MaybeSearch so we can suggest it later
		if len(f.Get("url").Errors()) > 0 && errors.Is(f.Get("url").Errors()[0], forms.ErrInvalidURL) {
			// User entered a wrong URL, we can mark it.
			tc["MaybeSearch"] = true
		}

		w.WriteHeader(http.StatusUnprocessableEntity)
	}

	// Retrieve the bookmark list
	bl := getBookmarkList(r.Context())

	tr := server.Locale(r)

	tc["Form"] = f
	tc["Pagination"] = bl.Pagination
	tc["Bookmarks"] = bl.Items
	tc["Filters"] = newContextFilterForm(r.Context(), tr)
	title := tr.Gettext("All your Bookmarks")

	if filters, ok := checkFilterForm(r.Context()); ok {
		tc["Filters"] = filters
		if filters.IsActive() {
			title = tr.Gettext("Bookmark Search")
		} else {
			switch filters.title {
			case filtersTitleUnread:
				title = tr.Gettext("Unread Bookmarks")
			case filtersTitleArchived:
				title = tr.Gettext("Archived Bookmarks")
			case filtersTitleFavorites:
				title = tr.Gettext("Favorite Bookmarks")
			case filtersTitleArticles:
				title = tr.Gettext("Articles")
			case filtersTitlePictures:
				title = tr.Gettext("Pictures")
			case filtersTitleVideos:
				title = tr.Gettext("Videos")
			}
		}
	}
	tc["PageTitle"] = title

	server.RenderTemplate(w, r, 200, "/bookmarks/index", tc)
}

func (h *viewsRouter) bookmarkInfo(w http.ResponseWriter, r *http.Request) {
	b := getBookmark(r.Context())
	item := dataset.NewBookmark(r.Context(), b)
	if err := item.SetEmbed(); err != nil {
		server.Log(r).Error("", slog.Any("err", err))
	}
	item.Errors = b.Errors

	if server.IsTurboRequest(r) {
		server.RenderTurboStream(w, r,
			"/bookmarks/components/card", "replace",
			"bookmark-card-"+b.UID, item, nil)
		return
	}

	html, err := item.GetArticle()
	if err != nil {
		server.Log(r).Error("", slog.Any("err", err))
	}

	// Set CSP for video playback
	if item.Type == "video" && item.EmbedHostname != "" {
		policy := server.GetCSPHeader(r).Clone()
		policy.Add("frame-src", item.EmbedHostname)
		policy.Write(w.Header())
	}

	server.RenderComponent(w, r, http.StatusOK, Views{}.bookmarkInfo(item, html))
}

func (h *viewsRouter) bookmarkCard(w http.ResponseWriter, r *http.Request) {
	if !server.IsTurboRequest(r) {
		server.TextMsg(w, r, http.StatusBadRequest, "not a turbo request")
		return
	}
	w.WriteHeader(http.StatusOK)

	b := getBookmark(r.Context())
	item := dataset.NewBookmark(r.Context(), b)

	server.RenderTurboStream(w, r,
		"/bookmarks/components/card", "replace",
		"bookmark-card-"+b.UID, item, nil)
}

func (h *viewsRouter) diagnosis(w http.ResponseWriter, r *http.Request) {
	if !server.IsTurboRequest(r) {
		server.TextMsg(w, r, http.StatusBadRequest, "not a turbo request")
		return
	}
	w.WriteHeader(http.StatusOK)

	b := getBookmark(r.Context())

	d, err := dataset.NewBokmarkDiagnosis(b)
	if err != nil {
		server.Err(w, r, err)
		return
	}

	server.RenderTurboStreamComponent(w, r,
		Components{}.diagnosis(d),
		"replace", "diagnosis-info", nil,
	)
}

func (h *viewsRouter) bookmarkUpdate(w http.ResponseWriter, r *http.Request) {
	tr := server.Locale(r)
	b := getBookmark(r.Context())

	tc := getBaseContext(r.Context())
	tc.SetBreadcrumbs([][2]string{
		{tr.Gettext("Bookmarks"), urls.AbsoluteURL(r, "/bookmarks").String()},
		{utils.ShortText(b.Title, 50), urls.AbsoluteURL(r, "/bookmarks", b.UID).String()},
		{tr.Gettext("Update")},
	})

	tc["Title"] = b.Title
	tc["ID"] = b.UID

	f := newUpdateForm(server.Locale(r))
	tc["Form"] = f

	status := http.StatusOK

	switch r.Method {
	case http.MethodGet:
		// Fields the user can update on the UI
		f.Get("title").Set(b.Title)
		f.Get("description").Set(b.Description)
		f.Get("site_name").Set(b.SiteName)
		f.Get("authors").Set([]string(b.Authors))
		f.Get("published").Set(b.Published)
		f.Get("lang").Set(b.Lang)
		f.Get("text_direction").Set(b.TextDirection)

	case http.MethodPost:
		forms.Bind(f, r)
		status = http.StatusUnprocessableEntity

		if !f.IsValid() {
			break
		}

		before := new(bookmarks.Bookmark)
		*before = *b

		updated, err := f.update(b)
		if err != nil {
			server.Log(r).Error("bookmark update", slog.Any("err", err))
			break
		}

		// On a turbo request, we'll return the updated components.
		if server.IsTurboRequest(r) {
			item := dataset.NewBookmark(r.Context(), b)

			_, withTitle := updated["title"]
			_, withDescription := updated["description"]
			_, withMarked := updated["is_marked"]
			_, withArchived := updated["is_archived"]
			_, withDeleted := updated["is_deleted"]
			_, withProgress := updated["read_progress"]

			if withTitle || withDescription {
				server.RenderTurboStream(w, r,
					"/bookmarks/components/title_block", "replace",
					"bookmark-title-"+b.UID, server.TC{"Item": item}, nil)
			}

			var beforeP, afterP time.Time
			if before.Published != nil {
				beforeP = *before.Published
			}
			if b.Published != nil {
				afterP = *b.Published
			}

			if before.SiteName != b.SiteName || beforeP != afterP || !slices.Equal(before.Authors, b.Authors) {
				server.RenderTurboStream(w, r,
					"/bookmarks/components/sidebar", "replace",
					"bookmark-sidebar-"+b.UID, server.TC{"Item": item}, nil)
			}

			if before.Lang != b.Lang || before.TextDirection != b.TextDirection {
				buf, err := item.GetArticle()
				if err != nil {
					server.Log(r).Error("", slog.Any("err", err))
				}
				server.RenderTurboStream(w, r,
					"/bookmarks/components/content_block", "replace",
					"bookmark-content-"+b.UID, map[string]any{
						"Item": item,
						"HTML": buf,
						"Out":  w,
					},
					nil,
				)
			}

			if !slices.Equal(before.Labels, b.Labels) {
				server.RenderTurboStream(w, r,
					"/bookmarks/components/labels", "replace",
					"bookmark-label-list-"+b.UID, item, nil)
			}

			if withMarked || withArchived || withDeleted || withProgress {
				server.RenderTurboStream(w, r,
					"/bookmarks/components/actions", "replace",
					"bookmark-actions-"+b.UID, item, nil)
				server.RenderTurboStream(w, r,
					"/bookmarks/components/card", "replace",
					"bookmark-card-"+b.UID, item, nil)
			}
			if withMarked || withArchived {
				server.RenderTurboStreamComponent(w, r,
					Components{}.bookmarkInfoBottomActions(item),
					"replace", "bookmark-bottom-actions-"+b.UID, nil,
				)
			}

			return
		}

		server.AddFlash(w, r, "success", tr.Gettext("Bookmark updated"))

		redir := "/bookmarks/" + b.UID + "/update"
		if f.Get("_to").String() != "" {
			redir = f.Get("_to").String()
		}
		server.Redirect(w, r, redir)

	}

	if server.IsTurboRequest(r) {
		w.WriteHeader(status)
		server.RenderTurboStream(w, r,
			"/bookmarks/components/bookmark_update", "replace",
			"bookmark-update-"+b.UID, tc, nil,
		)
		return
	}

	server.RenderTemplate(w, r, status, "bookmarks/bookmark_update", tc)
}

func (h *viewsRouter) bookmarkShareLink(w http.ResponseWriter, r *http.Request) {
	info := getSharedLink(r.Context())
	ctx := server.TC{
		"URL":     info.URL,
		"Expires": info.Expires,
		"Title":   info.Title,
		"ID":      info.ID,
	}

	if server.IsTurboRequest(r) {
		server.RenderTurboStream(w, r,
			"/bookmarks/components/share_link", "replace",
			"bookmark-share-"+info.ID, info, nil)
		return
	}

	server.RenderTemplate(w, r, http.StatusCreated, "bookmarks/bookmark_share_link", ctx)
}

func (h *viewsRouter) bookmarkShareEmail(w http.ResponseWriter, r *http.Request) {
	info := getSharedEmail(r.Context())
	tc := server.TC{
		"Form":  info.Form,
		"Title": info.Title,
		"ID":    info.ID,
		"Sent":  false,
	}

	// Get format from query string
	if format := r.URL.Query().Get("format"); format != "" {
		info.Form.Get("format").Set(format)
	}

	// Set default email address when sending an EPUB
	if u := auth.GetRequestUser(r); u != nil && info.Form.Get("format").String() == "epub" && info.Form.Get("email").String() == "" {
		info.Form.Get("email").Set(u.Settings.EmailSettings.EpubTo)
	}

	if r.Method == http.MethodPost {
		tc["Sent"] = info.Error == nil && info.Form.IsValid()
	}

	if server.IsTurboRequest(r) {
		server.RenderTurboStream(w, r,
			"/bookmarks/components/share_email", "replace",
			"bookmark-share-"+info.ID, tc, nil)
		return
	}

	server.RenderTemplate(w, r, http.StatusOK, "bookmarks/bookmark_share_email", tc)
}

func (h *viewsRouter) labelList(w http.ResponseWriter, r *http.Request) {
	tc := getBaseContext(r.Context())
	tc["Labels"] = getLabelList(r.Context())

	server.RenderTemplate(w, r, 200, "/bookmarks/labels", tc)
}

func (h *viewsRouter) labelInfo(w http.ResponseWriter, r *http.Request) {
	bl := getBookmarkList(r.Context())
	label := getLabel(r.Context())

	if bl.Pagination.TotalCount == 0 {
		server.Status(w, r, http.StatusNotFound)
		return
	}

	// POST, update label name
	if r.Method == http.MethodPost {
		f := newLabelForm(server.Locale(r))
		forms.Bind(f, r)

		if f.IsValid() {
			_, err := bookmarks.Bookmarks.RenameLabel(auth.GetRequestUser(r), label, f.Get("name").String())
			if err != nil {
				server.Err(w, r, err)
				return
			}

			redir := urls.AbsoluteURL(r, "/bookmarks/labels")
			redir.RawQuery = url.Values{"name": []string{f.Get("name").String()}}.Encode()
			w.Header().Set("Location", redir.String())
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
	}

	tc := getBaseContext(r.Context())
	tc["Label"] = label
	tc["Pagination"] = bl.Pagination
	tc["Bookmarks"] = bl.Items
	tc["IsDeleted"] = tasks.DeleteLabelTask.IsRunning(fmt.Sprintf("%d@%s", auth.GetRequestUser(r).ID, label))

	server.RenderTemplate(w, r, 200, "/bookmarks/label", tc)
}

func (h *viewsRouter) labelDelete(w http.ResponseWriter, r *http.Request) {
	bl := getBookmarkList(r.Context())
	label := getLabel(r.Context())

	if bl.Pagination.TotalCount == 0 {
		server.Status(w, r, http.StatusNotFound)
		return
	}

	f := newLabelDeleteForm(server.Locale(r))
	forms.Bind(f, r)
	if err := f.trigger(auth.GetRequestUser(r), label); err != nil {
		server.Err(w, r, err)
		return
	}

	// We can't use redirect here, since we must escape the label
	redir := urls.AbsoluteURL(r, "/bookmarks/labels")
	redir.RawQuery = url.Values{"name": []string{label}}.Encode()
	w.Header().Set("Location", redir.String())
	w.WriteHeader(http.StatusSeeOther)
}

func (h *viewsRouter) annotationList(w http.ResponseWriter, r *http.Request) {
	al := getAnnotationList(r.Context())

	server.SendPaginationHeaders(w, r, al.Pagination)

	tc := getBaseContext(r.Context())
	tc["Pagination"] = al.Pagination
	tc["Annotations"] = al.Items

	server.RenderTemplate(w, r, 200, "/bookmarks/annotation_list", tc)
}

func (h *publicViewsRouter) withBookmark(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := chi.URLParam(r, "id")
		id, expires, err := bookmarks.DecodeID(data)
		if err != nil {
			server.Log(r).Warn("shared bookmark", slog.Any("err", err))
			server.Status(w, r, 404)
			return
		}

		expired := expires.Before(time.Now().UTC())
		status := http.StatusOK
		ct := server.TC{
			"Expired": expired,
		}

		if !expired {
			var bu struct {
				User     *users.User         `db:"u"`
				Bookmark *bookmarks.Bookmark `db:"b"`
			}
			ds := bookmarks.Bookmarks.
				Query().
				Join(goqu.T(db.TableUser).As("u"), goqu.On(goqu.I("u.id").Eq(goqu.I("b.user_id")))).
				Where(
					goqu.I("b.id").Eq(id),
					goqu.I("b.state").Eq(bookmarks.StateLoaded),
				)
			found, err := ds.ScanStruct(&bu)

			if !found || err != nil {
				status = http.StatusNotFound
			} else {
				// Don't show the notes on a shared, public, page.
				ctx := dataset.WithAnnotationTag(r.Context(),
					dataset.AnnotationTag,
					dataset.AnnotationCallback(false),
				)

				item := dataset.NewBookmark(ctx, bu.Bookmark)
				if err := item.SetEmbed(); err != nil {
					server.Err(w, r, err)
					return
				}
				ct["Username"] = bu.User.Username
				ct["Item"] = item

				w.Header().Add("readeck-original", item.URL)
				server.NewLink(item.URL).WithRel("original").Write(w)
				server.NewLink(item.URL).WithRel("cite-as").Write(w)
				server.WriteLastModified(w, r, bu.Bookmark)
				server.WriteEtag(w, r, bu.Bookmark)
			}
		} else {
			status = http.StatusGone
		}

		ct["Status"] = status

		ctx := withBaseContext(r.Context(), ct)
		server.WithCaching(next).ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *publicViewsRouter) get(w http.ResponseWriter, r *http.Request) {
	tc := getBaseContext(r.Context())
	status := tc["Status"].(int)

	if status == http.StatusOK {
		item := tc["Item"].(*dataset.Bookmark)
		article, err := item.GetArticle()
		if err != nil {
			server.Err(w, r, err)
			return
		}

		tc["HTML"] = article

		// Harden CSP
		policy := server.GetCSPHeader(r).Clone()
		policy.Set("connect-src", csp.None)
		policy.Set("form-action", csp.None)

		// Relax CSP for video playback
		if item.Type == "video" && item.EmbedHostname != "" {
			policy.Add("frame-src", item.EmbedHostname)
		}
		policy.Write(w.Header())
	}

	server.RenderTemplate(w, r, status, "bookmarks/bookmark_public", tc)
}
