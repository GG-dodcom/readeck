// SPDX-FileCopyrightText: © 2020 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/pkg/forms"
)

// PaginationForm is a default form for pagination.
type PaginationForm struct {
	forms.Form
	Limit  forms.IntegerField `json:"limit"  validate:"gte:0 lte:100"`
	Offset forms.IntegerField `json:"offset" validate:"gte:0"`
}

// GetPageParams returns the pagination parameters from the query string.
func GetPageParams(r *http.Request, defaultLimit int) *PaginationForm {
	f := forms.New[PaginationForm](r.Context())
	forms.BindValues(r.URL.Query(), f)

	if !f.IsValid() {
		return nil
	}

	if f.Limit.Value() == 0 {
		f.Limit.Set(defaultLimit)
	}

	return f
}

// Pagination holds all the information regarding pagination.
type Pagination struct {
	URL          *url.URL
	Limit        int
	Offset       int
	TotalCount   int
	TotalPages   int
	CurrentPage  int
	First        int
	Last         int
	Next         int
	Previous     int
	FirstPage    string
	LastPage     string
	NextPage     string
	PreviousPage string
	PageLinks    []PageLink
}

// PageLink contains a link to a page in a Pagination instance.
type PageLink struct {
	Index int
	URL   string
}

// GetLink returns a new url string with limit and offset values.
func (p Pagination) GetLink(offset int) string {
	u := *p.URL
	q := u.Query()
	q.Set("limit", strconv.Itoa(p.Limit))
	q.Set("offset", strconv.Itoa(offset))
	u.RawQuery = q.Encode()
	return u.String()
}

// GetPageLinks returns the links that can be used in a template.
func (p Pagination) GetPageLinks() []PageLink {
	res := []PageLink{
		{1, p.GetLink(0)},
	}

	prevLinks := []PageLink{}
	for i := p.CurrentPage - 1; i > max(1, p.CurrentPage-3); i-- {
		prevLinks = append([]PageLink{{i, p.GetLink((i - 1) * p.Limit)}}, prevLinks...)
	}
	if len(prevLinks) > 0 && prevLinks[0].Index > 2 {
		res = append(res, PageLink{})
	}
	res = append(res, prevLinks...)

	if p.CurrentPage > 1 {
		res = append(res, PageLink{p.CurrentPage, p.GetLink((p.CurrentPage - 1) * p.Limit)})
	}

	for i := p.CurrentPage + 1; i < min(p.TotalPages, p.CurrentPage+3); i++ {
		res = append(res, PageLink{i, p.GetLink((i - 1) * p.Limit)})
	}

	if len(res) > 0 && res[len(res)-1].Index < p.TotalPages-1 {
		res = append(res, PageLink{})
	}

	if p.CurrentPage < p.TotalPages {
		res = append(res, PageLink{p.TotalPages, p.GetLink(p.Last)})
	}

	return res
}

// NewPagination creates a new Pagination instance base on the current request.
func NewPagination(ctx context.Context, count, limit, offset int) Pagination {
	p := Pagination{
		URL:         urls.AbsoluteURLContext(ctx),
		Limit:       limit,
		Offset:      offset,
		TotalCount:  count,
		TotalPages:  int(math.Ceil(float64(count) / float64(limit))),
		CurrentPage: int(math.Floor(float64(offset)/float64(limit))) + 1,
		First:       0,
	}
	if p.TotalPages > 0 {
		p.Last = (p.TotalPages - 1) * p.Limit
	}

	if n := p.Offset + p.Limit; n <= p.Last {
		p.Next = p.Offset + p.Limit
		p.NextPage = p.GetLink(p.Next)
	}
	if n := p.Offset - p.Limit; n >= 0 {
		p.Previous = p.Offset - p.Limit
		p.PreviousPage = p.GetLink(p.Previous)
	}

	p.FirstPage = p.GetLink(p.First)
	if p.Last > 0 {
		p.LastPage = p.GetLink(p.Last)
	}
	p.PageLinks = p.GetPageLinks()

	return p
}

// GetPaginationLinks returns a list of Link instances suitable for pagination.
func GetPaginationLinks(_ *http.Request, p Pagination) []Link {
	links := []Link{}

	if p.PreviousPage != "" {
		links = append(links, NewLink(p.PreviousPage).WithRel("previous"))
	}
	if p.NextPage != "" {
		links = append(links, NewLink(p.NextPage).WithRel("next"))
	}
	if p.FirstPage != "" {
		links = append(links, NewLink(p.FirstPage).WithRel("first"))
	}
	if p.LastPage != "" {
		links = append(links, NewLink(p.LastPage).WithRel("last"))
	}

	return links
}

// SendPaginationHeaders compute and set the pagination headers.
func SendPaginationHeaders(
	w http.ResponseWriter, r *http.Request,
	p Pagination,
) {
	pages := int(math.Ceil(float64(p.TotalCount) / float64(p.Limit)))
	page := int(math.Floor(float64(p.Offset)/float64(p.Limit))) + 1

	for _, link := range GetPaginationLinks(r, p) {
		link.Write(w)
	}

	w.Header().Set("Total-Count", strconv.Itoa(p.TotalCount))
	w.Header().Set("Total-Pages", strconv.Itoa(pages))
	w.Header().Set("Current-Page", strconv.Itoa(page))
}
