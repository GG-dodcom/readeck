// SPDX-FileCopyrightText: © 2020 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/bookmarks/converter"
	"codeberg.org/readeck/readeck/internal/bookmarks/dataset"
	"codeberg.org/readeck/readeck/internal/db/exp"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/annotate"
	"codeberg.org/readeck/readeck/pkg/forms"
	forms2 "codeberg.org/readeck/readeck/pkg/forms/v2"
	"codeberg.org/readeck/readeck/pkg/http/csp"
	"codeberg.org/readeck/readeck/pkg/zipfs"
)

// bookmarkList renders a paginated list of the connected
// user bookmarks in JSON.
func (api *apiRouter) bookmarkList(w http.ResponseWriter, r *http.Request) {
	bl := getBookmarkList(r.Context())

	server.SendPaginationHeaders(w, r, bl.Pagination)
	server.Render(w, r, http.StatusOK, bl.Items)
}

// bookmarkInfo renders a given bookmark items in JSON.
func (api *apiRouter) bookmarkInfo(w http.ResponseWriter, r *http.Request) {
	b := getBookmark(r.Context())
	item := dataset.NewBookmark(r.Context(), b)
	if err := item.SetEmbed(); err != nil {
		server.Log(r).Error("", slog.Any("err", err))
	}
	item.Errors = b.Errors

	server.Render(w, r, http.StatusOK, item)
}

// bookmarkArticle renders the article HTML content of a bookmark.
// Note that only the body's content is rendered.
func (api *apiRouter) bookmarkArticle(w http.ResponseWriter, r *http.Request) {
	b := getBookmark(r.Context())

	bi := dataset.NewBookmark(r.Context(), b)
	buf, err := bi.GetArticle()
	if err != nil {
		server.Log(r).Error("", slog.Any("err", err))
	}

	if server.IsTurboRequest(r) {
		server.RenderTurboStreamComponent(w, r,
			Components{}.articleContent(bi, buf),
			"replace", "bookmark-content-"+b.UID,
			map[string]string{
				"method": "morph",
			})
		server.RenderTurboStreamComponent(w, r,
			SidebarComponent{}.annotationList(bi),
			"replace", "bookmark-annotation-list-"+b.UID,
			map[string]string{
				"method": "morph",
			})
		return
	}

	// Set link headers for all article's resources.
	c, err := b.OpenContainer()
	if err != nil {
		server.Log(r).Error("", slog.Any("err", err))
	}
	defer c.Close() //nolint:errchec

	for _, x := range c.ListResources() {
		server.NewLink(
			urls.AbsoluteURL(r, "/bm", b.FilePath, x.Name).String(),
		).
			WithRel("media").
			Write(w)
	}

	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	io.Copy(w, buf)
}

func (api *apiRouter) bookmarkListFeed(w http.ResponseWriter, r *http.Request) {
	bl := getBookmarkList(r.Context())

	ctx := dataset.WithURLReplacer(context.Background(), func(b *bookmarks.Bookmark) func(name string) string {
		return func(name string) string {
			return urls.AbsoluteURL(r, "/bm", b.FilePath, name).String()
		}
	})

	if _, ok := auth.GetRequestProvider(r).(*server.SessionAuthProvider); ok {
		// Add an XSL stylesheet.
		ctx = converter.WithAtomStylesheet(ctx, urls.PathOnly(urls.AbsoluteURL(r, "/assets/feed.xsl")))

		// The stylesheet is considered a script by browsers,
		// so we need to relax the CSP for it to work.
		policy := server.GetCSPHeader(r).Clone()
		policy.Set("style-src", csp.Self)
		policy.Set("script-src", csp.Self)
		policy.Write(w.Header())
	}

	if err := converter.NewAtomExporter().Export(ctx, w, r, bl); err != nil {
		server.Err(w, r, err)
	}
}

// bookmarkExport renders a list of bookmarks in the requested export format.
func (api *apiRouter) bookmarkExport(w http.ResponseWriter, r *http.Request) {
	var exporter converter.IterExporter
	format := chi.URLParam(r, "format")
	switch format {
	case "csv":
		exporter = converter.CSVExporter{}
	case "html":
		exporter = converter.BrowserExporter{}
	case "epub":
		exp := converter.NewEPUBExporter()
		if collection, ok := checkCollection(r.Context()); ok {
			exp.Collection = collection
		}
		exporter = exp
	case "md.zip":
		// Support the special "md.zip" extension that forces the request for a zipfile
		// and then move on to the next, markdown, case.
		r.Header.Set("Accept", "application/zip")
		fallthrough
	case "md":
		exporter = converter.NewMarkdownExporter()
	}

	if exporter == nil {
		server.TextMsg(w, r, http.StatusNotAcceptable, "unknow format: "+format)
		return
	}

	seq := getBookmarkIterator(r.Context())
	count, err := seq.Count()
	if err != nil {
		server.Err(w, r, err)
		return
	}
	if count == 0 {
		server.Status(w, r, http.StatusNotFound)
		return
	}

	ctx := converter.WithEnableEPUBNotes(r.Context(), true)
	if err := exporter.IterExport(ctx, w, r, seq); err != nil {
		server.Err(w, r, err)
	}
}

// bookmarkCreate creates a new bookmark.
func (api *apiRouter) bookmarkCreate(w http.ResponseWriter, r *http.Request) {
	f := forms2.BindAs[createForm](r)

	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	b, err := f.createBookmark(r)
	if err != nil {
		server.Err(w, r, err)
		return
	}

	w.Header().Add(
		"Location",
		urls.AbsoluteURL(r, ".", b.UID).String(),
	)
	w.Header().Add("bookmark-id", b.UID)
	server.NewLink(urls.AbsoluteURL(r, "/bookmarks", b.UID).String()).
		WithRel("alternate").
		WithType("text/html").
		Write(w)

	server.TextMsg(w, r, http.StatusAccepted, "Link submited")
}

// bookmarkUpdate updates an existing bookmark.
func (api *apiRouter) bookmarkUpdate(w http.ResponseWriter, r *http.Request) {
	f := forms2.BindAs[updateForm](r)

	if !f.IsValid() {
		server.Render(w, r, http.StatusBadRequest, f)
		return
	}

	b := getBookmark(r.Context())

	updated, err := f.update(b)
	if err != nil {
		server.Err(w, r, err)
		return
	}

	updated["href"] = urls.AbsoluteURL(r).String()

	if server.IsTurboRequest(r) {
		// We don't want to render any content on a turbo request.
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Add("Location", updated["href"].(string))
	server.Render(w, r, http.StatusOK, updated)
}

// bookmarkDelete deletes a bookmark.
func (api *apiRouter) bookmarkDelete(w http.ResponseWriter, r *http.Request) {
	b := getBookmark(r.Context())
	if err := b.Delete(); err != nil {
		server.Err(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// bookmarkShareLink returns a publicly shared bookmark link.
func (api *apiRouter) bookmarkShareLink(w http.ResponseWriter, r *http.Request) {
	info := getSharedLink(r.Context())
	server.Render(w, r, http.StatusCreated, info)
}

// bookmarkShareEmail sends a bookmark by email.
func (api *apiRouter) bookmarkShareEmail(w http.ResponseWriter, r *http.Request) {
	info := getSharedEmail(r.Context())
	if !info.Form.IsValid() {
		server.Render(w, r, 0, info.Form) // status is already set by the middleware
		return
	}

	server.TextMsg(w, r, http.StatusOK, "Email sent to "+info.Form.Get("email").String())
}

// bookmarkResource is the route returning any resource
// from a given bookmark. The resource is extracted from
// the sidecar zip file of a bookmark.
// Note that for images, we'll use another route that is not
// authenticated and thus, much faster.
func (api *apiRouter) bookmarkResource(w http.ResponseWriter, r *http.Request) {
	b := getBookmark(r.Context())
	p := path.Clean(chi.URLParam(r, "*"))

	r2 := new(http.Request)
	*r2 = *r
	r2.URL = new(url.URL)
	*r2.URL = *r.URL
	r2.URL.Path = p

	fs := zipfs.HTTPZipFile(b.GetFilePath())
	fs.ServeHTTP(w, r2)
}

func (api *apiRouter) autocompleteHelper(w http.ResponseWriter, r *http.Request) {
	f := newPropLookupForm(server.Locale(r))
	forms.BindURL(f, r)
	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	values := []string{}
	ds := f.getQuerySet(auth.GetRequestUser(r))
	if ds != nil {
		if err := ds.ScanVals(&values); err != nil {
			server.Err(w, r, err)
			return
		}
	}
	server.Render(w, r, http.StatusOK, values)
}

// labelList returns the list of all labels.
func (api *apiRouter) labelList(w http.ResponseWriter, r *http.Request) {
	labels := getLabelList(r.Context())
	server.Render(w, r, http.StatusOK, labels)
}

// labelInfo return the information about a label.
func (api *apiRouter) labelInfo(w http.ResponseWriter, r *http.Request) {
	label := getLabel(r.Context())
	ds := bookmarks.Bookmarks.GetLabels().
		Where(
			goqu.C("user_id").Table("b").Eq(auth.GetRequestUser(r).ID),
			goqu.I("name").Eq(label),
		)

	res := new(dataset.Label)
	exists, err := ds.ScanStruct(res)
	if err != nil {
		server.Err(w, r, err)
		return
	}
	if !exists {
		server.Status(w, r, http.StatusNotFound)
		return
	}
	dataset.NewLabel(r.Context(), res)
	server.Render(w, r, http.StatusOK, res)
}

func (api *apiRouter) labelUpdate(w http.ResponseWriter, r *http.Request) {
	label := getLabel(r.Context())
	f := newLabelForm(server.Locale(r))
	forms.Bind(f, r)

	if !f.IsValid() {
		server.Render(w, r, http.StatusBadRequest, f)
		return
	}

	ids, err := bookmarks.Bookmarks.RenameLabel(auth.GetRequestUser(r), label, f.Get("name").String())
	if err != nil {
		server.Err(w, r, err)
		return
	}
	if len(ids) == 0 {
		server.Status(w, r, http.StatusNotFound)
		return
	}

	server.Render(w, r, http.StatusOK, map[string]string{
		"name": f.Get("name").String(),
	})
}

func (api *apiRouter) labelDelete(w http.ResponseWriter, r *http.Request) {
	label := getLabel(r.Context())

	ids, err := bookmarks.Bookmarks.RenameLabel(auth.GetRequestUser(r), label, "")
	if err != nil {
		server.Err(w, r, err)
		return
	}
	if len(ids) == 0 {
		server.Status(w, r, http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (api *apiRouter) bookmarkAnnotations(w http.ResponseWriter, r *http.Request) {
	b := getBookmark(r.Context())
	if b.Annotations != nil {
		server.Render(w, r, http.StatusOK, b.Annotations)
		return
	}

	server.Render(w, r, http.StatusOK, bookmarks.BookmarkAnnotations{})
}

func (api *apiRouter) annotationCreate(w http.ResponseWriter, r *http.Request) {
	b := getBookmark(r.Context())
	f := forms2.New[annotationForm](r.Context()) // newAnnotationForm(server.Locale(r))
	forms2.Bind(r, f)
	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	bi := dataset.NewBookmark(r.Context(), b)
	annotation, err := f.addToBookmark(bi)
	if err != nil {
		if errors.As(err, &annotate.ErrAnotate) {
			server.Msg(w, r, &server.Message{
				Status:  http.StatusBadRequest,
				Message: err.Error(),
			})
		} else {
			server.Err(w, r, err)
		}
		return
	}

	w.Header().Add("Location", urls.AbsoluteURL(r, ".", annotation.ID).String())
	server.Render(w, r, http.StatusCreated, annotation)
}

func (api *apiRouter) annotationUpdate(w http.ResponseWriter, r *http.Request) {
	b := getBookmark(r.Context())
	id := chi.URLParam(r, "id")
	if b.Annotations == nil {
		server.Status(w, r, http.StatusNotFound)
		return
	}

	if b.Annotations.Get(id) == nil {
		server.Status(w, r, http.StatusNotFound)
		return
	}

	f := forms2.New[annotationUpdateForm](r.Context()) // newAnnotationUpdateForm(server.Locale(r))
	forms2.Bind(r, f)
	if !f.IsValid() {
		server.Render(w, r, http.StatusUnprocessableEntity, f)
		return
	}

	annotation := b.Annotations.Get(id)
	annotation.Color = f.Color.Value()
	if f.Note.IsBound() {
		annotation.Note = f.Note.Value()
	}
	update := map[string]any{
		"annotations": b.Annotations,
	}
	err := b.Update(update)
	if err != nil {
		server.Err(w, r, err)
		return
	}

	server.Render(w, r, http.StatusOK, update)
}

func (api *apiRouter) annotationDelete(w http.ResponseWriter, r *http.Request) {
	b := getBookmark(r.Context())
	id := chi.URLParam(r, "id")
	if b.Annotations == nil {
		server.Status(w, r, http.StatusNotFound)
		return
	}
	if b.Annotations.Get(id) == nil {
		server.Status(w, r, http.StatusNotFound)
		return
	}

	b.Annotations.Delete(id)
	err := b.Update(map[string]any{
		"annotations": b.Annotations,
	})
	if err != nil {
		server.Err(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (api *apiRouter) annotationList(w http.ResponseWriter, r *http.Request) {
	al := getAnnotationList(r.Context())

	server.SendPaginationHeaders(w, r, al.Pagination)
	server.Render(w, r, 200, al.Items)
}

// withBookmark returns a router that will fetch a bookmark and add it into the
// request's context. It also deals with if-modified-since header.
func (api *apiRouter) withBookmark(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := chi.URLParam(r, "uid")

		b, err := bookmarks.Bookmarks.GetOne(
			goqu.C("uid").Eq(uid),
			goqu.C("user_id").Eq(auth.GetRequestUser(r).ID),
		)
		if err != nil {
			server.Status(w, r, http.StatusNotFound)
			return
		}
		ctx := withBookmark(r.Context(), b)

		if b.State == bookmarks.StateLoaded {
			server.WriteLastModified(w, r, b, auth.GetRequestUser(r))
			server.WriteEtag(w, r, b, auth.GetRequestUser(r))
		}

		w.Header().Add("bookmark-id", b.UID)
		server.NewLink(urls.AbsoluteURL(r, "/bookmarks", b.UID).String()).
			WithRel("alternate").
			WithType("text/html").
			Write(w)

		server.WithCaching(next).ServeHTTP(w, r.WithContext(ctx))
	})
}

func (api *apiRouter) withBookmarkFilters(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filter := chi.URLParam(r, "filter")
		filters := newFilterForm(server.Locale(r))

		switch filter {
		case "unread":
			filters.setArchived(false)
		case "archives":
			filters.setArchived(true)
		case "favorites":
			filters.setMarked(true)
		case "articles":
			filters.setType("article")
		case "pictures":
			filters.setType("photo")
		case "videos":
			filters.setType("video")
		}

		next.ServeHTTP(w, r.WithContext(withFilterForm(r.Context(), filters)))
	})
}

func (api *apiRouter) withLabel(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		label := strings.TrimSpace(r.URL.Query().Get("name"))
		if label == "" {
			server.Status(w, r, http.StatusNotFound)
			return
		}
		ctx := withLabel(r.Context(), label)

		filters := newFilterForm(server.Locale(r))
		filters.Get("labels").Set(strconv.Quote(label))
		ctx = withFilterForm(ctx, filters)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (api *apiRouter) withDefaultLimit(limit int) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := withDefaultLimit(r.Context(), limit)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (api *apiRouter) withoutPagination(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := newContextFilterForm(r.Context(), server.Locale(r))
		f.noPagination = true
		next.ServeHTTP(w, r.WithContext(withFilterForm(r.Context(), f)))
	})
}

func (api *apiRouter) withFixedLimit(limit uint) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f := newContextFilterForm(r.Context(), server.Locale(r))
			f.fixedLimit = limit
			next.ServeHTTP(w, r.WithContext(withFilterForm(r.Context(), f)))
		})
	}
}

func (api *apiRouter) withCollectionFilters(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c *bookmarks.Collection
		var ok bool
		var err error
		ctx := r.Context()
		c, ok = checkCollection(ctx)
		if !ok {
			// No collection in context, let's see if we have an ID
			uid := r.URL.Query().Get("collection")
			if uid == "" {
				next.ServeHTTP(w, r)
				return
			}

			c, err = bookmarks.Collections.GetOne(
				goqu.C("uid").Eq(uid),
				goqu.C("user_id").Eq(auth.GetRequestUser(r).ID),
			)
			if err != nil {
				server.Status(w, r, http.StatusNotFound)
				return
			}
			ctx = withCollection(r.Context(), c)
		}

		// Apply filters
		f := newCollectionForm(server.Locale(r), r)
		f.setCollection(c)
		filters := newContextFilterForm(r.Context(), server.Locale(r))
		f.setFilters(filters)
		ctx = withFilterForm(ctx, filters)

		if _, ok := checkBookmarkOrder(ctx); !ok {
			ctx = withBookmarkOrder(ctx, orderExpressionList{goqu.T("b").Col("created").Desc()})
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (api *apiRouter) withBookmarkOrdering(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := newBookmarkOrderForm()
		forms.BindURL(f, r)

		ctx := withOrderForm(r.Context(), f)
		order := f.toOrderedExpressions()
		if order != nil {
			ctx = withBookmarkOrder(ctx, order)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (api *apiRouter) withBookmarkListSelectDataset(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, ok := checkDefaultLimit(r.Context())
		if !ok {
			limit = 50
		}

		pf := server.GetPageParams(r, limit)
		if pf == nil {
			server.Status(w, r, http.StatusNotFound)
			return
		}

		ds := bookmarks.Bookmarks.Query().
			Select(
				"b.id", "b.uid", "b.created", "b.updated", "b.published", "b.state",
				"b.url", "b.title", "b.domain", "b.site", "b.site_name", "b.authors",
				"b.lang", "b.dir", "b.type", "b.is_marked", "b.is_archived", "b.read_progress",
				"b.labels", "b.description", "b.word_count", "b.duration", "b.file_path", "b.files",
				"b.annotations").
			Where(
				goqu.C("user_id").Table("b").Eq(auth.GetRequestUser(r).ID),
			)

		ds = ds.Order(exp.DateTime(goqu.I("created")).Desc())

		// Filters (search and other filterForm)
		filterForm := newContextFilterForm(r.Context(), server.Locale(r))
		forms.BindURL(filterForm, r)

		if !filterForm.IsValid() {
			// When the form is invalid and we're not in a view, render the form's error list.
			// Not having the counters a good indicator for that, as they are only
			// set for views in [viewsRouter.withBaseContext].
			if _, ok := checkCounters(r.Context()); !ok {
				server.Render(w, r, http.StatusUnprocessableEntity, filterForm)
				return
			}
		} else {
			filters := bookmarks.NewFiltersFromForm(filterForm)
			filters.UpdateForm(filterForm)
			ds = filters.ToSelectDataSet(ds)
		}

		// Filtering by ids. In this case we include all the given IDs and we sort the
		// result according to the IDs order.
		if !filterForm.Get("id").IsNil() {
			ids := filterForm.Get("id").Value().([]string)
			ds = ds.Where(goqu.C("uid").Table("b").In(ids))

			orderging := goqu.Case().Value(goqu.C("uid").Table("b"))
			for i, x := range ids {
				orderging = orderging.When(x, goqu.Cast(goqu.V(i), "NUMERIC"))
			}
			ds = ds.Order(orderging.Asc())
		}

		ds = ds.
			Limit(uint(pf.Limit.Value())).
			Offset(uint(pf.Offset.Value()))

		// Apply sorting given by a query string
		if order, ok := checkBookmarkOrder(r.Context()); ok {
			ds = ds.Order(order...)
		}

		// If pagination is disabled, remove all limits.
		if filterForm.noPagination {
			ds = ds.ClearLimit().ClearOffset()
		}

		// If fixed limit is set, override limit and offset
		if filterForm.fixedLimit > 0 {
			ds = ds.Limit(uint(filterForm.fixedLimit)).Offset(0)
		}

		// Save filters in context
		ctx := withFilterForm(r.Context(), filterForm)

		// Save select dataset
		ctx = withBookmarkListDS(ctx, ds)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (api *apiRouter) withBookmarkSeq(next http.Handler) http.Handler {
	serve := func(w http.ResponseWriter, r *http.Request, ds *goqu.SelectDataset) {
		res := dataset.NewBookmarkIterator(r.Context(), ds)
		ctx := withBookmarkIterator(r.Context(), res)

		taggers := []server.Etagger{res}
		if t, ok := checkBookmarkListTaggers(r.Context()); ok {
			taggers = append(taggers, t...)
		}

		if r.Method == http.MethodGet {
			server.WriteEtag(w, r, taggers...)
		}

		server.WithCaching(next).ServeHTTP(w, r.WithContext(ctx))
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := chi.URLParam(r, "uid")
		if uid != "" {
			ds := bookmarks.Bookmarks.Query().Where(
				goqu.C("user_id").Table("b").Eq(auth.GetRequestUser(r).ID),
				goqu.C("uid").Table("b").Eq(uid),
			)
			serve(w, r, ds)
			return
		}

		api.withBookmarkListSelectDataset(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ds := getBookmarkListDS(r.Context())
			serve(w, r, ds)
		})).ServeHTTP(w, r)
	})
}

func (api *apiRouter) withBookmarkList(next http.Handler) http.Handler {
	return api.withBookmarkListSelectDataset(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ds := getBookmarkListDS(r.Context())

		var res *dataset.BookmarkList
		var err error
		if res, err = dataset.NewBookmarkList(r.Context(), ds); err != nil {
			if errors.Is(err, bookmarks.ErrBookmarkNotFound) {
				server.TextMsg(w, r, http.StatusNotFound, "not found")
			} else {
				server.Err(w, r, err)
			}
			return
		}

		ctx := withBookmarkList(r.Context(), res)

		taggers := []server.Etagger{res}
		if t, ok := checkBookmarkListTaggers(r.Context()); ok {
			taggers = append(taggers, t...)
		}

		if r.Method == http.MethodGet {
			server.WriteEtag(w, r, taggers...)
		}
		server.WithCaching(next).ServeHTTP(w, r.WithContext(ctx))
	}))
}

func (api *apiRouter) withAnnotationList(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, ok := checkDefaultLimit(r.Context())
		if !ok {
			limit = 50
		}

		pf := server.GetPageParams(r, limit)
		if pf == nil {
			server.Status(w, r, http.StatusNotFound)
			return
		}

		ds := bookmarks.Bookmarks.GetAnnotations().
			Where(
				goqu.C("user_id").Table("b").Eq(auth.GetRequestUser(r).ID),
			)

		ds = ds.
			Limit(uint(pf.Limit.Value())).
			Offset(uint(pf.Offset.Value())).
			Order(exp.DateTime(goqu.I("annotation_created")).Desc())

		res, err := dataset.NewAnnotationList(r.Context(), ds)
		if err != nil {
			server.Err(w, r, err)
			return
		}
		ctx := withAnnotationList(r.Context(), res)

		server.WriteEtag(w, r, res)
		server.WithCaching(next).ServeHTTP(w, r.WithContext(ctx))
	})
}

func (api *apiRouter) withLabelList(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ds := bookmarks.Bookmarks.GetLabels().
			Where(
				goqu.C("user_id").Table("b").Eq(auth.GetRequestUser(r).ID),
			)

		f := newLabelSearchForm(server.Locale(r))
		forms.BindURL(f, r)
		if f.Get("q").String() != "" {
			q := strings.ReplaceAll(f.Get("q").String(), "*", "%")
			ds = ds.Where(goqu.I("name").ILike(q))
		}

		res, err := dataset.NewLabelList(r.Context(), ds)
		if err != nil {
			server.Err(w, r, err)
			return
		}

		ctx := withLabelList(r.Context(), res)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (api *apiRouter) withShareLink(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Disable HTTP caching
		server.WriteLastModified(w, r)
		server.WriteEtag(w, r)

		b := getBookmark(r.Context())
		if b.State != bookmarks.StateLoaded {
			server.Err(w, r, errors.New("bookmark not loaded yet"))
			return
		}

		expires := time.Now().UTC().Round(time.Minute).Add(
			time.Duration(configs.Config.Bookmarks.PublicShareTTL) * time.Hour,
		)

		rr, err := bookmarks.EncodeID(uint64(b.ID), expires)
		if err != nil {
			server.Err(w, r, err)
			return
		}

		info := dataset.SharedLink{
			URL:     urls.AbsoluteURL(r, "/@b", rr).String(),
			Expires: expires,
			Title:   b.Title,
			ID:      b.UID,
		}
		ctx := withSharedLink(r.Context(), info)
		w.Header().Set("Location", info.URL)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (api *apiRouter) withShareEmail(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Disable HTTP caching
		server.WriteLastModified(w, r)
		server.WriteEtag(w, r)

		b := getBookmark(r.Context())
		if b.State != bookmarks.StateLoaded {
			server.Err(w, r, errors.New("bookmark not loaded yet"))
			return
		}

		info := dataset.SharedEmail{
			Form:  newShareForm(server.Locale(r)),
			Title: b.Title,
			ID:    b.UID,
		}

		if r.Method == http.MethodPost {
			forms.Bind(info.Form, r)

			if info.Form.IsValid() {
				info.Error = info.Form.(*shareForm).sendBookmark(r, b)
			}
			if info.Error != nil {
				server.Log(r).Error("could not send email", slog.Any("err", info.Error))
			}
			if !info.Form.IsValid() {
				w.WriteHeader(http.StatusUnprocessableEntity)
			}
			info.Sent = true
		}

		ctx := withSharedEmail(r.Context(), info)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
