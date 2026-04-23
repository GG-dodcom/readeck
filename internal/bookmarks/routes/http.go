// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package routes provides http routes
// for bookmarks and collections management.
package routes

import (
	"net/http"
	"net/url"
	"path"

	"github.com/go-chi/chi/v5"

	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/pkg/http/csp"
	"codeberg.org/readeck/readeck/pkg/zipfs"
)

// apiRouter is the base bookmark API router.
type apiRouter struct {
	chi.Router
}

type viewsRouter struct {
	chi.Router
	*apiRouter
}

type publicViewsRouter struct {
	chi.Router
	*apiRouter
}

// SetupRoutes mounts the routes for the bookmarks domain.
// "/bm" is a public route outside the api scope in order to avoid
// sending the session cookie.
func SetupRoutes(s *server.Server) {
	// Routes
	// Saved bookmark resources (images & all)
	s.AddRoute("/bm", mediaRoutes(s))

	// API routes
	api := newAPIRouter()
	s.AddRoute("/api/bookmarks", api)

	// Website routes
	s.AddRoute("/bookmarks", newViewsRouter(api))

	// Publicly shared bookmark
	s.AddRoute("/@b", newSharedViewsRouter(api))
}

// newAPIRouter returns an apiRouter with all the routes set up.
func newAPIRouter() *apiRouter {
	r := server.AuthenticatedRouter()

	api := &apiRouter{r}

	// Bookmark API
	r.With(server.WithPermission("api:bookmarks", "read")).Group(func(r chi.Router) {
		r.With(
			api.withBookmarkOrdering,
			api.withCollectionFilters,
			api.withBookmarkList,
		).Get("/", api.bookmarkList)
		r.With(api.withBookmark).Route("/{uid:[a-zA-Z0-9]{18,22}}", func(r chi.Router) {
			r.Get("/", api.bookmarkInfo)
			r.Get("/article", api.bookmarkArticle)
			r.Get("/annotations", api.bookmarkAnnotations)
			r.With(server.WithPermission("api:bookmarks", "share")).Route(
				"/share", func(r chi.Router) {
					r.With(
						api.withShareLink,
					).Get("/link", api.bookmarkShareLink)
					r.With(
						server.WithPermission("email", "send"),
						api.withShareEmail,
					).Post("/email", api.bookmarkShareEmail)
				})
			r.Get("/x/*", api.bookmarkResource)
		})

		r.With(api.withAnnotationList).Route("/annotations", func(r chi.Router) {
			r.Get("/", api.annotationList)
		})

		r.With(server.WithPermission("api:bookmarks", "export")).Group(func(r chi.Router) {
			r.With(
				api.withFixedLimit(25),
				api.withCollectionFilters,
				api.withBookmarkList,
			).Get("/feed", api.bookmarkListFeed)
			r.With(
				api.withoutPagination,
				api.withBookmarkOrdering,
				api.withCollectionFilters,
				api.withBookmarkSeq,
			).Get("/export.{format}", api.bookmarkExport)
			r.With(
				api.withBookmarkSeq,
			).Get("/{uid:[a-zA-Z0-9]{18,22}}/article.{format}", api.bookmarkExport)
		})

		r.Route("/sync", func(r chi.Router) {
			r.Get("/", api.bookmarkSyncList)
			r.Post("/", api.bookmarkSync)
		})

		// We use the same route for the label list and label info
		// for a label can be anything and it can clash with path
		// parameters (ie. "#/../" is a valid label but will break resolution).
		// We split the resolution based on the presence of a "name" query
		// parameter.
		r.Get("/labels", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Has("name") {
				chi.Chain(api.withLabel).HandlerFunc(api.labelInfo).ServeHTTP(w, r)
				return
			}
			chi.Chain(api.withLabelList).HandlerFunc(api.labelList).ServeHTTP(w, r)
		})

		r.Get("/@complete", api.autocompleteHelper)
	})

	r.With(server.WithPermission("api:bookmarks", "write")).Group(func(r chi.Router) {
		r.Post("/", api.bookmarkCreate)
		r.With(api.withBookmark).Group(func(r chi.Router) {
			r.Patch("/{uid:[a-zA-Z0-9]{18,22}}", api.bookmarkUpdate)
			r.Delete("/{uid:[a-zA-Z0-9]{18,22}}", api.bookmarkDelete)
			r.Post("/{uid:[a-zA-Z0-9]{18,22}}/annotations", api.annotationCreate)
			r.Patch("/{uid:[a-zA-Z0-9]{18,22}}/annotations/{id:[a-zA-Z0-9]{18,22}}", api.annotationUpdate)
			r.Delete("/{uid:[a-zA-Z0-9]{18,22}}/annotations/{id:[a-zA-Z0-9]{18,22}}", api.annotationDelete)
		})

		// Using withLabel ensures that the query parameter is present.
		r.With(api.withLabel).Patch("/labels", api.labelUpdate)
		r.With(api.withLabel).Delete("/labels", api.labelDelete)
	})

	// Collection API
	r.Route("/collections", func(r chi.Router) {
		r.With(server.WithPermission("api:bookmarks:collections", "read")).Group(func(r chi.Router) {
			r.With(api.withColletionList).Get("/", api.collectionList)
			r.With(api.withCollection).Get("/{uid:[a-zA-Z0-9]{18,22}}", api.collectionInfo)
		})

		r.With(server.WithPermission("api:bookmarks:collections", "write")).Group(func(r chi.Router) {
			r.Post("/", api.collectionCreate)
			r.With(api.withCollection).Patch("/{uid:[a-zA-Z0-9]{18,22}}", api.collectionUpdate)
			r.With(api.withCollection).Delete("/{uid:[a-zA-Z0-9]{18,22}}", api.collectionDelete)
		})
	})

	// Import API
	r.Route("/import", func(r chi.Router) {
		r.With(server.WithPermission("api:bookmarks", "import")).Group(func(r chi.Router) {
			r.Get("/{trackID:[a-zA-Z0-9]{18,22}}", api.bookmaksImportStatus)
			r.Post("/{source}", api.bookmarksImport)
		})
	})

	return api
}

// newViewsRouter returns a viewRouter with all the routes set up.
func newViewsRouter(api *apiRouter) *viewsRouter {
	r := server.AuthenticatedRouter(server.WithRedirectLogin)

	h := &viewsRouter{r, api}

	// Bookmark and label views
	r.With(server.WithPermission("bookmarks", "read")).Group(func(r chi.Router) {
		r.With(h.withBaseContext, api.withDefaultLimit(listDefaultLimit)).Group(func(r chi.Router) {
			r.With(
				api.withBookmarkOrdering,
				api.withBookmarkList,
			).Get("/", h.bookmarkList)
			r.With(
				api.withBookmarkFilters,
				api.withBookmarkOrdering,
				api.withBookmarkList,
			).Get("/{filter:(unread|archives|favorites|articles|videos|pictures)}", h.bookmarkList)

			r.With(
				server.WithCustomErrorComponent(404, Views{}.bookmarkMissing),
				api.withBookmark,
			).Route("/{uid:[a-zA-Z0-9]{18,22}}", func(r chi.Router) {
				r.Get("/", h.bookmarkInfo)
				r.Get("/card", h.bookmarkCard)
				r.Get("/diagnosis", h.diagnosis)
				r.With(server.WithPermission("bookmarks", "share")).Route(
					"/share", func(r chi.Router) {
						r.With(
							api.withShareLink,
						).Get("/link", h.bookmarkShareLink)
						r.With(
							server.WithPermission("email", "send"),
							api.withShareEmail,
						).Route("/email", func(r chi.Router) {
							r.Get("/", h.bookmarkShareEmail)
							r.Post("/", h.bookmarkShareEmail)
						})
					})
			})

			r.With(api.withAnnotationList).Route("/highlights", func(r chi.Router) {
				r.Get("/", h.annotationList)
			})

			// Same logic as the /labels API route.
			r.Get("/labels", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Has("name") {
					chi.Chain(
						api.withLabel, api.withBookmarkOrdering, api.withBookmarkList,
					).HandlerFunc(h.labelInfo).ServeHTTP(w, r)
					return
				}
				chi.Chain(api.withLabelList).HandlerFunc(h.labelList).ServeHTTP(w, r)
			})
		})
	})

	r.With(server.WithPermission("bookmarks", "write")).Group(func(r chi.Router) {
		r.With(h.withBaseContext, api.withDefaultLimit(listDefaultLimit)).Group(func(r chi.Router) {
			r.With(api.withBookmarkList).Post("/", h.bookmarkList)
			r.With(api.withBookmark).Group(func(r chi.Router) {
				r.Get("/{uid:[a-zA-Z0-9]{18,22}}/update", h.bookmarkUpdate)
				r.Post("/{uid:[a-zA-Z0-9]{18,22}}/update", h.bookmarkUpdate)
			})

			r.With(api.withLabel, api.withBookmarkList).Group(func(r chi.Router) {
				r.Post("/labels", h.labelInfo)
				r.Post("/labels/delete", h.labelDelete)
			})
		})
	})

	// Collection views
	r.Route("/collections", func(r chi.Router) {
		r.With(server.WithPermission("bookmarks:collections", "read")).Group(func(r chi.Router) {
			r.With(h.withBaseContext, api.withDefaultLimit(listDefaultLimit)).Group(func(r chi.Router) {
				r.With(api.withColletionList).Get("/", h.collectionList)
				r.With(
					api.withCollection,
					api.withCollectionFilters,
					api.withBookmarkOrdering,
					api.withBookmarkList,
				).Get("/{uid:[a-zA-Z0-9]{18,22}}", h.collectionInfo)
			})
		})

		r.With(server.WithPermission("bookmarks:collections", "write")).Group(func(r chi.Router) {
			r.With(h.withBaseContext, api.withDefaultLimit(listDefaultLimit)).Group(func(r chi.Router) {
				r.With(api.withBookmarkList).Get("/add", h.collectionCreate)
				r.With(api.withBookmarkList).Post("/add", h.collectionCreate)
				r.With(
					api.withCollection,
					api.withCollectionFilters,
					api.withBookmarkList,
				).Post("/{uid:[a-zA-Z0-9]{18,22}}", h.collectionInfo)
				r.With(
					api.withCollection,
				).Post("/{uid:[a-zA-Z0-9]{18,22}}/delete", h.collectionDelete)
			})
		})
	})

	// Import views
	r.Route("/import", func(r chi.Router) {
		r.With(server.WithPermission("bookmarks", "import")).Group(func(r chi.Router) {
			r.With(h.withBaseContext).Group(func(r chi.Router) {
				r.Get("/", h.bookmarksImportMain)
				r.Get("/{trackID:[a-zA-Z0-9]{18,22}}", h.bookmarksImportMain)
				r.Get("/{source}", h.bookmarksImport)
				r.Post("/{source}", h.bookmarksImport)
			})
		})
	})

	return h
}

func newSharedViewsRouter(api *apiRouter) *publicViewsRouter {
	r := chi.NewRouter()
	h := &publicViewsRouter{r, api}

	r.With(h.withBookmark).Get("/{id:[a-zA-Z0-9_-]+}", h.get)
	return h
}

// mediaRoutes serves files from a bookmark's saved archive. It reads
// directly from the zip file and returns the requested file's content.
func mediaRoutes(_ *server.Server) http.Handler {
	r := chi.NewRouter()
	r.Get("/{prefix:[a-zA-Z0-9]{2}}/{fname:[a-zA-Z0-9]+}/{p:^(img|_resources)$}/{name}", func(w http.ResponseWriter, r *http.Request) {
		p := path.Join(
			chi.URLParam(r, "p"),
			chi.URLParam(r, "name"),
		)
		p = path.Clean(p)

		r2 := new(http.Request)
		*r2 = *r
		r2.URL = new(url.URL)
		*r2.URL = *r.URL
		r2.URL.Path = p

		zipfile := path.Join(
			bookmarks.StoragePath(),
			chi.URLParam(r, "prefix"),
			chi.URLParam(r, "fname")+".zip",
		)

		fs := zipfs.HTTPZipFile(zipfile)
		fs.ServeHTTP(w, r2, func(w http.ResponseWriter, status int) {
			// Anything that comes from a bookmark resource needs a strict policy
			// We allow unsafe-inline for SVG embed styles
			csp.Policy{
				"base-uri":    {csp.None},
				"default-src": {csp.None},
				"style-src":   {csp.UnsafeInline},
			}.Write(w.Header())

			if status == http.StatusOK {
				w.Header().Set("Cache-Control", `public, max-age=31536000`)
			}
		})
	})

	return r
}
