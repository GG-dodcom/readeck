// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/components"
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

		ctx := withBaseContext(r.Context(), c) // FIXME: remove
		ctx = withCounters(ctx, count)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *viewsRouter) bookmarkList(w http.ResponseWriter, r *http.Request) {
	f := newCreateForm(r)

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
		w.WriteHeader(http.StatusUnprocessableEntity)
	}

	// Retrieve the bookmark list
	bl := getBookmarkList(r.Context())

	tr := server.Locale(r)
	title := tr.Gettext("All your Bookmarks")
	filters := newContextFilterForm(r.Context(), tr)

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

	server.RenderComponent(w, r, http.StatusOK, Views{}.bookmarkList(
		title, bl, f, filters,
	))
}

func (h *viewsRouter) bookmarkInfo(w http.ResponseWriter, r *http.Request) {
	b := getBookmark(r.Context())
	item := dataset.NewBookmark(r.Context(), b)
	if err := item.SetEmbed(); err != nil {
		server.Log(r).Error("", slog.Any("err", err))
	}
	item.Errors = b.Errors

	if server.IsTurboRequest(r) {
		server.RenderTurboStreamComponent(w, r,
			Components{}.card(item),
			"replace", "bookmark-card-"+b.UID, nil)
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

	server.RenderTurboStreamComponent(w, r,
		Components{}.card(item),
		"replace", "bookmark-card-"+b.UID, nil)
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
	f := newUpdateForm(server.Locale(r))

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
				server.RenderTurboStreamComponent(w, r,
					Components{}.titleBlock(item),
					"replace", "bookmark-title-"+b.UID, nil,
				)
			}

			var beforeP, afterP time.Time
			if before.Published != nil {
				beforeP = *before.Published
			}
			if b.Published != nil {
				afterP = *b.Published
			}

			if before.SiteName != b.SiteName || beforeP != afterP || !slices.Equal(before.Authors, b.Authors) {
				server.RenderTurboStreamComponent(w, r,
					SidebarComponent{}.sidebar(item),
					"replace", "bookmark-sidebar-"+b.UID, nil)
			}

			if before.Lang != b.Lang || before.TextDirection != b.TextDirection {
				buf, err := item.GetArticle()
				if err != nil {
					server.Log(r).Error("", slog.Any("err", err))
				}
				server.RenderTurboStreamComponent(w, r,
					Components{}.articleContent(item, buf),
					"replace", "bookmark-content-"+b.UID,
					map[string]string{
						"method": "morph",
					})
			}

			if !slices.Equal(before.Labels, b.Labels) {
				server.RenderTurboStreamComponent(w, r,
					SidebarComponent{}.labelList(item),
					"replace", "bookmark-label-list-"+b.UID, nil)
			}

			if withMarked || withArchived || withDeleted || withProgress {
				server.RenderTurboStreamComponent(w, r,
					SidebarComponent{}.actions(item),
					"replace", "bookmark-actions-"+b.UID, nil)
				server.RenderTurboStreamComponent(w, r,
					Components{}.card(item),
					"replace", "bookmark-card-"+b.UID, nil)
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
		return
	}

	if server.IsTurboRequest(r) {
		w.WriteHeader(status)
		server.RenderTurboStreamComponent(w, r,
			Components{}.bookmarkUpdate(b, f),
			"replace", "bookmark-update-"+b.UID, nil,
		)
		return
	}

	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Bookmarks"), urls.AbsoluteURL(r, "/bookmarks").String()},
		{utils.ShortText(b.Title, 50), urls.AbsoluteURL(r, "/bookmarks", b.UID).String()},
		{tr.Gettext("Update")},
	})

	server.RenderComponent(w, r.WithContext(ctx), status, Views{}.bookmarkUpdate(b, f))
}

func (h *viewsRouter) bookmarkShareLink(w http.ResponseWriter, r *http.Request) {
	link := getSharedLink(r.Context())
	if server.IsTurboRequest(r) {
		server.RenderTurboStreamComponent(w, r,
			Components{}.shareByLink(link),
			"replace", "bookmark-share-"+link.ID, nil)

		return
	}

	tr := server.Locale(r)
	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Bookmarks"), urls.AbsoluteURL(r, "/bookmarks").String()},
		{utils.ShortText(link.Title, 50), urls.AbsoluteURL(r, "/bookmarks", link.ID).String()},
		{tr.Gettext("Share link")},
	})
	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.shareByLink(link))
}

func (h *viewsRouter) bookmarkShareEmail(w http.ResponseWriter, r *http.Request) {
	info := getSharedEmail(r.Context())

	// Get format from query string
	if format := r.URL.Query().Get("format"); format != "" {
		info.Form.Get("format").Set(format)
	}

	// Set default email address when sending an EPUB
	if u := auth.GetRequestUser(r); u != nil && info.Form.Get("format").String() == "epub" && info.Form.Get("email").String() == "" {
		info.Form.Get("email").Set(u.Settings.EmailSettings.EpubTo)
	}

	if server.IsTurboRequest(r) {
		server.RenderTurboStreamComponent(w, r,
			Components{}.shareByEmail(info), "replace",
			"bookmark-share-"+info.ID, nil)
		return
	}

	tr := server.Locale(r)
	ctx := components.WithBreadcrumb(r.Context(), [][2]string{
		{tr.Gettext("Bookmarks"), urls.AbsoluteURL(r, "/bookmarks").String()},
		{utils.ShortText(info.Title, 50), urls.AbsoluteURL(r, "/bookmarks", info.ID).String()},
		{tr.Gettext("Share by email")},
	})
	server.RenderComponent(w, r.WithContext(ctx), http.StatusOK, Views{}.shareByEmail(info))
}

func (h *viewsRouter) labelList(w http.ResponseWriter, r *http.Request) {
	server.RenderComponent(
		w, r, http.StatusOK,
		Views{}.labelList(getLabelList(r.Context())),
	)
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

	isDeleted := tasks.DeleteLabelTask.IsRunning(fmt.Sprintf("%d@%s", auth.GetRequestUser(r).ID, label))
	server.RenderComponent(w, r, http.StatusOK, Views{}.labelInfo(
		label, bl, isDeleted,
	))
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
	server.RenderComponent(
		w, r, http.StatusOK,
		Views{}.annotationList(getAnnotationList(r.Context())),
	)
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
