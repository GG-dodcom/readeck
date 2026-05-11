// SPDX-FileCopyrightText: © 2021 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package routes

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	goquexp "github.com/doug-martin/goqu/v9/exp"
	"golang.org/x/text/language"

	"codeberg.org/readeck/readeck/internal/auth"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/bookmarks/converter"
	"codeberg.org/readeck/readeck/internal/bookmarks/dataset"
	"codeberg.org/readeck/readeck/internal/bookmarks/tasks"
	"codeberg.org/readeck/readeck/internal/db"
	"codeberg.org/readeck/readeck/internal/db/exp"
	"codeberg.org/readeck/readeck/internal/email"
	"codeberg.org/readeck/readeck/locales"
	"codeberg.org/readeck/readeck/pkg/forms"
	"codeberg.org/readeck/readeck/pkg/http/request"
	"codeberg.org/readeck/readeck/pkg/utils"
)

var (
	errNoResourceURL     = errors.New("no resource URL")
	errNoResourceContent = errors.New("No resource content")
)

const (
	filtersTitleUnset = iota
	filtersTitleUnread
	filtersTitleArchived
	filtersTitleFavorites
	filtersTitleArticles
	filtersTitleVideos
	filtersTitlePictures
)

const (
	filtersReadStatusUnread  = "unread"
	filtersReadStatusReading = "reading"
	filtersReadStatusRead    = "read"
)

type orderExpressionList []goquexp.OrderedExpression

type multipartResourceInfo struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// LoadMultipartResource loads a [forms.FileOpener] into the provided [tasks.MultipartResource].
// Unless the resource already has a URL property, the file part MUST provide a Location header
// with the URL of the resource to load.
// Alternatively it can be the previously supported format that contains a JSON payload
// on the first line (with the url and headers) and the data on the remaining lines.
func LoadMultipartResource(opener forms.FileOpener, res *tasks.MultipartResource) error {
	var r io.ReadCloser
	r, err := opener.Open()
	if err != nil {
		return err
	}
	defer r.Close() // nolint:errcheck

	readContent := func(r io.Reader) error {
		res.Data, err = io.ReadAll(r)
		if err != nil {
			return err
		}
		if len(res.Data) == 0 {
			return errNoResourceContent
		}
		return nil
	}

	// Never carry empty headers
	if res.Header == nil {
		res.Header = make(http.Header)
	}

	// URL defined? load the body and return
	if res.URL != "" {
		return readContent(r)
	}

	const bufSize = 256 << 10 // In KiB
	bio := bufio.NewReaderSize(r, bufSize)

	location := opener.Header().Get("Location")
	if location == "" {
		// no location given, it must be a legacy format with a JSON starting line
		if c, _ := bio.Peek(1); len(c) > 0 && c[0] == '{' {
			var line []byte
			if line, err = bio.ReadBytes('\n'); err != nil {
				return err
			}
			info := new(multipartResourceInfo)
			if err = json.Unmarshal(line, info); err != nil {
				return err
			}
			if info.URL == "" {
				return errNoResourceURL
			}
			res.URL = info.URL
			for k, v := range info.Headers {
				res.Header.Set(k, v)
			}
			return readContent(bio)
		}

		return errNoResourceURL
	}

	// New format, using part headers directly
	res.URL = location
	maps.Copy(res.Header, opener.Header())
	res.Header.Del("Content-Disposition")
	res.Header.Del("Location")

	return readContent(bio)
}

type createForm struct {
	forms.Form
	URL       forms.TextField     `json:"url"               validate:"trim required max_len:1024 is_url"`
	Title     forms.TextField     `json:"title"             validate:"trim max_len:1024"`
	Labels    forms.TextListField `json:"labels"            validate:"trim discard_empty"`
	Created   forms.DatetimeField `json:"created"`
	FindMain  forms.BooleanField  `json:"feature_find_main"`
	HTML      forms.FileField     `json:"html"`
	Resources forms.FileListField `json:"resource"`

	resources []tasks.MultipartResource
}

func (f *createForm) Validate() error {
	if !f.IsValid() {
		return nil
	}

	// Load the html when provided.
	if f.HTML.IsBound() && !f.HTML.IsEmpty() {
		opener := f.HTML.Value()

		// The html field is exactly a resource for the main page,
		// set its properties now so we only need to read its content.
		resource := tasks.MultipartResource{
			URL: f.URL.Value(),
			Header: http.Header{
				"Content-Type": {"text/html"},
			},
		}
		if err := LoadMultipartResource(opener, &resource); err != nil {
			return forms.Gettext("Unable to process input data")
		}

		f.resources = append(f.resources, resource)

		// This is final and "resource" values are ignored.
		return nil
	}

	// Load all the resources passed in the "resource" field.
	for _, opener := range f.Resources.Value() {
		resource := tasks.MultipartResource{}
		err := LoadMultipartResource(opener, &resource)
		if err != nil {
			if f.URL.Value() != resource.URL {
				// As long as the error is not from the requested URL we can ignore it.
				continue
			}
			return forms.Gettext("Unable to process input data")
		}
		f.resources = append(f.resources, resource)
	}

	return nil
}

func (f *createForm) createBookmark(r *http.Request) (b *bookmarks.Bookmark, err error) {
	if !f.IsBound() {
		return nil, errors.New("form is not bound")
	}

	uri, _ := url.Parse(f.URL.Value())
	uri.Fragment = ""

	b = &bookmarks.Bookmark{
		UserID:   &(auth.GetUser(r.Context()).ID),
		State:    bookmarks.StateLoading,
		URL:      uri.String(),
		Title:    f.Title.Value(),
		Site:     uri.Hostname(),
		SiteName: uri.Hostname(),
	}

	if len(f.Labels.Value()) > 0 {
		b.Labels = f.Labels.Value()
		slices.Sort(b.Labels)
		b.Labels = slices.Compact(b.Labels)
	}

	if !f.Created.Value().IsZero() {
		b.Created = f.Created.Value().UTC()
	}

	defer func() {
		if err != nil {
			f.AddErrors(forms.ErrUnexpected)
		}
	}()

	if err = bookmarks.Bookmarks.Create(b); err != nil {
		return nil, err
	}

	// Start extraction job
	err = tasks.ExtractPageTask.Run(b.ID, tasks.ExtractParams{
		BookmarkID: b.ID,
		RequestID:  request.GetReqID(r.Context()),
		Resources:  f.resources,
		FindMain:   !f.FindMain.IsBound() || f.FindMain.Value(),
	})
	if err != nil {
		return nil, err
	}

	return b, nil
}

type updateForm struct {
	forms.Form
	Title         forms.TextField     `json:"title"          validate:"trim required_or_nil max_len:1024"`
	Description   forms.TextField     `json:"description"    validate:"trim"`
	SiteName      forms.TextField     `json:"site_name"      validate:"trim required_or_nil"`
	Authors       forms.TextListField `json:"authors"        validate:"trim discard_empty split_lines"`
	Published     forms.DatetimeField `json:"published"`
	Lang          forms.TextField     `json:"lang"           validate:"trim lowercase check_lang"`
	TextDirection forms.TextField     `json:"text_direction" validate:"text_dir_choices"`
	IsMarked      forms.BooleanField  `json:"is_marked"`
	IsArchived    forms.BooleanField  `json:"is_archived"`
	IsDeleted     forms.BooleanField  `json:"is_deleted"`
	ReadProgress  forms.IntegerField  `json:"read_progress"  validate:"gte:0 lte:100"`
	ReadAnchor    forms.TextField     `json:"read_anchor"    validate:"trim max_len:256"`
	Labels        forms.TextListField `json:"labels"         validate:"trim discard_empty"`
	AddLabels     forms.TextListField `json:"add_labels"     validate:"trim discard_empty"`
	RemoveLabels  forms.TextListField `json:"remove_labels"  validate:"trim discard_empty"`
	To            forms.TextField     `json:"_to"            validate:"trim max_len:512"`
}

func (f *updateForm) GetTaggedValidator(name, _ string, tc *forms.TagContext) (forms.Validator, bool) {
	switch name {
	case "lowercase":
		return forms.CleanerFunc[string](strings.ToLower), true
	case "check_lang":
		return forms.TypedValidator(func(v string) bool {
			if v == "" {
				return true
			}
			_, err := language.Parse(v)
			return err == nil
		}, forms.Gettext("invalid language code")), true
	case "text_dir_choices":
		forms.Choices(tc.Field,
			forms.Choice("", ""),
			forms.Choice(forms.GetTranslator(tc.Context).Gettext("Left to right"), "ltr"),
			forms.Choice(forms.GetTranslator(tc.Context).Gettext("Right to left"), "rtl"),
		)
		return nil, true
	default:
		return nil, false
	}
}

func (f *updateForm) update(b *bookmarks.Bookmark) (updated map[string]any, err error) {
	updated = map[string]any{}
	var deleted *bool
	labelsChanged := false

	if f.IsDeleted.IsBound() {
		deleted = new(f.IsDeleted.Value())
	}

	if f.Title.IsBound() {
		b.Title = utils.NormalizeSpaces(f.Title.Value())
		updated["title"] = b.Title
	}

	if f.Description.IsBound() {
		b.Description = utils.NormalizeSpaces(f.Description.Value())
		updated["description"] = b.Description
	}

	if f.SiteName.IsBound() {
		b.SiteName = utils.NormalizeSpaces(f.SiteName.Value())
		updated["site_name"] = b.SiteName
	}

	if f.Published.IsBound() {
		if f.Published.Value().IsZero() {
			b.Published = nil
			updated["published"] = nil
		} else {
			b.Published = new(f.Published.Value().UTC())
			updated["published"] = b.Published
		}
	}

	if f.Lang.IsBound() {
		b.Lang = f.Lang.Value()
		updated["lang"] = b.Lang
	}

	if f.TextDirection.IsBound() {
		b.TextDirection = f.TextDirection.Value()
		updated["text_direction"] = b.TextDirection
	}

	if f.Authors.IsBound() {
		b.Authors = f.Authors.Value()
		updated["authors"] = b.Authors
	}

	if f.IsMarked.IsBound() {
		b.IsMarked = f.IsMarked.Value()
		updated["is_marked"] = b.IsMarked
	}

	if f.IsArchived.IsBound() {
		b.IsArchived = f.IsArchived.Value()
		updated["is_archived"] = b.IsArchived
	}

	if f.ReadProgress.IsBound() {
		b.ReadProgress = f.ReadProgress.Value()
		updated["read_progress"] = b.ReadProgress
	}

	if f.ReadAnchor.IsBound() {
		b.ReadAnchor = f.ReadAnchor.Value()
		updated["read_anchor"] = b.ReadAnchor
	}

	// labels, add_labels and remove_labels are declared and
	// processed in this order.
	if f.Labels.IsBound() {
		b.Labels = f.Labels.Value()
		labelsChanged = true
	}

	if f.AddLabels.IsBound() {
		b.Labels = append(b.Labels, f.AddLabels.Value()...)
		labelsChanged = true
	}

	if f.RemoveLabels.IsBound() {
		b.Labels = slices.DeleteFunc(b.Labels, func(s string) bool {
			return slices.Contains(f.RemoveLabels.Value(), s)
		})
		labelsChanged = true
	}

	if labelsChanged {
		slices.SortFunc(b.Labels, db.UnaccentCompare)
		b.Labels = slices.Compact(b.Labels)
		updated["labels"] = b.Labels
	}

	if _, ok := updated["read_progress"]; ok {
		if b.ReadProgress == 0 || b.ReadProgress == 100 {
			b.ReadAnchor = ""
			updated["read_anchor"] = ""
		}
	}

	defer func() {
		updated["id"] = b.UID
		if err != nil {
			f.AddErrors(forms.ErrUnexpected)
		}
	}()

	if len(updated) > 0 || deleted != nil {
		if _, ok := updated["text_direction"]; ok {
			updated["dir"] = updated["text_direction"]
			delete(updated, "text_direction")
		}

		updated["updated"] = time.Now().UTC()
		if err = b.Update(updated); err != nil {
			return
		}

		if _, ok := updated["dir"]; ok {
			updated["text_direction"] = updated["dir"]
			delete(updated, "dir")
		}
	}

	if deleted != nil {
		updated["is_deleted"] = *deleted
		err = f.delete(b, !*deleted)
	}

	return
}

func (f *updateForm) delete(b *bookmarks.Bookmark, cancel bool) error {
	if cancel {
		return tasks.DeleteBookmarkTask.Cancel(b.ID)
	}
	return tasks.DeleteBookmarkTask.Run(b.ID, b.ID)
}

type syncListForm struct {
	forms.Form
	Since forms.DatetimeField `json:"since"`
}

type syncForm struct {
	forms.Form
	OrderForm

	ID             forms.TextListField `json:"id"`
	WithJSON       forms.BooleanField  `json:"with_json"`
	WithHTML       forms.BooleanField  `json:"with_html"`
	WithMarkdown   forms.BooleanField  `json:"with_markdown"`
	WithResources  forms.BooleanField  `json:"with_resources"`
	ResourcePrefix forms.TextField     `json:"resource_prefix" validate:"max_len:128"`
}

func newSyncForm(ctx context.Context) *syncForm {
	f := forms.New[syncForm](ctx)
	f.setSortChoices(map[string]goquexp.Orderable{
		"updated": exp.DateTime(goqu.C("updated")),
		"created": exp.DateTime(goqu.C("created")),
	})

	f.ResourcePrefix.Set(".")

	return f
}

type autocompleteHelperForm struct {
	forms.Form
	Type  forms.TextField `json:"type" validate:"required type_choices"`
	Query forms.TextField `json:"q"    validate:"trim required"`
}

func (f *autocompleteHelperForm) GetTaggedValidator(name, _ string, tc *forms.TagContext) (forms.Validator, bool) {
	switch name {
	case "type_choices":
		forms.Choices(tc.Field,
			forms.Choice("author", "author"),
			forms.Choice("label", "label"),
			forms.Choice("site", "site"),
			forms.Choice("title", "title"),
		)
		return nil, true
	default:
		return nil, false
	}
}

func (f *autocompleteHelperForm) getQuerySet(user *users.User) *goqu.SelectDataset {
	q := strings.ReplaceAll(f.Query.Value(), "*", "%")

	switch f.Type.Value() {
	case "author":
		return exp.JSONStringsDataset(db.Q().
			From(goqu.T(db.TableBookmark).As("b")).
			Select(goqu.C("authors").Table("b")),
			"name",
		).
			Distinct().
			Where(
				goqu.C("user_id").Table("b").Eq(user.ID),
				goqu.C("name").ILike(q),
			).
			Prepared(true)
	case "label":
		return exp.JSONStringsDataset(db.Q().
			From(goqu.T(db.TableBookmark).As("b")).
			Select(goqu.C("labels").Table("b")),
			"name",
		).
			Distinct().
			Where(
				goqu.C("user_id").Table("b").Eq(user.ID),
				goqu.C("name").ILike(q),
			).
			Prepared(true)
	case "site":
		d1 := db.Q().From(goqu.T(db.TableBookmark).As("b")).
			Select(
				goqu.C("domain"),
			).
			Distinct().
			Where(
				goqu.C("user_id").Table("b").Eq(user.ID),
				goqu.I("domain").ILike(q),
			)
		d2 := db.Q().From(goqu.T(db.TableBookmark).As("b")).
			Select(
				goqu.C("site_name"),
			).
			Distinct().
			Where(
				goqu.C("user_id").Table("b").Eq(user.ID),
				goqu.I("site_name").ILike(q),
			)

		return d1.Union(d2).Prepared(true)
	case "title":
		return db.Q().From(goqu.T(db.TableBookmark).As("b")).
			Select(
				goqu.C("title"),
			).
			Distinct().
			Where(
				goqu.C("user_id").Table("b").Eq(user.ID),
				goqu.C("title").Table("b").ILike(q),
				goqu.C("title").Table("b").Neq(""),
			).
			Order(goqu.C("title").Asc()).
			Prepared(true)
	}

	return nil
}

type labelForm struct {
	forms.Form
	Name forms.TextField `json:"name" validate:"trim required"`
}

type labelSearchForm struct {
	forms.Form
	Query forms.TextField `json:"q" validate:"trim required_or_nil"`
}

type labelDeleteForm struct {
	forms.Form
	Cancel forms.BooleanField `json:"cancel"`
}

func (f *labelDeleteForm) trigger(user *users.User, name string) error {
	id := fmt.Sprintf("%d@%s", user.ID, name)

	if f.Cancel.IsBound() && f.Cancel.Value() {
		return tasks.DeleteLabelTask.Cancel(id)
	}

	return tasks.DeleteLabelTask.Run(id, tasks.LabelDeleteParams{
		UserID: user.ID, Name: name,
	})
}

// OrderForm is a form providing a "sort" parameter and methods
// to set a query's ORDER BY clause.
type OrderForm struct {
	forms.Form
	choices map[string]goquexp.Orderable
	Sort    forms.TextListField `json:"sort" validate:"trim"`
}

func newOrderForm(ctx context.Context, choices map[string]goquexp.Orderable) *OrderForm {
	f := forms.New[OrderForm](ctx)
	f.setSortChoices(choices)
	return f
}

func (f *OrderForm) setSortChoices(choices map[string]goquexp.Orderable) {
	// Compile a list of choices being pairs of "A" and "-A", "B", "-B",
	fieldChoices := make(forms.ValueChoices[string], 0, len(choices)*2)
	for k := range choices {
		fieldChoices = append(fieldChoices, forms.Choice("", k), forms.Choice("", "-"+k))
	}
	f.choices = choices
	forms.Choices(&f.Sort, fieldChoices...)
}

func (f *OrderForm) toOrderedExpressions() orderExpressionList {
	values := f.Sort.Value()
	if len(values) == 0 {
		return nil
	}

	res := orderExpressionList{}
	for _, x := range values {
		identifier := f.choices[strings.TrimPrefix(x, "-")]
		if identifier == nil {
			continue
		}
		if strings.HasPrefix(x, "-") {
			res = append(res, identifier.Desc())
			continue
		}
		res = append(res, identifier.Asc())
	}

	return res
}

type bookmarkOrderForm struct {
	OrderForm
}

func newBookmarkOrderForm(ctx context.Context) *bookmarkOrderForm {
	t := goqu.T("b")

	return &bookmarkOrderForm{*newOrderForm(ctx, map[string]goquexp.Orderable{
		"created":   exp.DateTime(t.Col("created")),
		"domain":    t.Col("domain"),
		"duration":  goqu.Case().When(goqu.L("? > 0", t.Col("duration")), t.Col("duration")).Else(goqu.L("? * 0.3", t.Col("word_count"))),
		"published": exp.DateTime(goqu.Case().When(t.Col("published").IsNot(nil), t.Col("published")).Else(t.Col("created"))),
		"site":      t.Col("site_name"),
		"title":     t.Col("title"),
	})}
}

func (f *bookmarkOrderForm) getOptions(r *http.Request, tr *locales.Locale) [][3]string {
	qs := url.Values{}
	for k, v := range r.URL.Query() {
		if k == "sort" {
			continue
		}
		qs[k] = v
	}

	setOption := func(name, label string) [3]string {
		qs["sort"] = []string{name}
		defer delete(qs, "sort")
		return [3]string{name, r.URL.Path + "?" + qs.Encode(), label}
	}

	return [][3]string{
		setOption("-created", tr.Pgettext("sort", "Added, most recent first")),
		setOption("created", tr.Pgettext("sort", "Added, oldest first")),
		setOption("-published", tr.Pgettext("sort", "Published, most recent first")),
		setOption("published", tr.Pgettext("sort", "Published, oldest first")),
		setOption("title", tr.Pgettext("sort", "Title, A to Z")),
		setOption("-title", tr.Pgettext("sort", "Title, Z to A")),
		setOption("site", tr.Pgettext("sort", "Site Name, A to Z")),
		setOption("-site", tr.Pgettext("sort", "Site Name, Z to A")),
		setOption("duration", tr.Pgettext("sort", "Duration, shortest first")),
		setOption("-duration", tr.Pgettext("sort", "Duration, longest first")),
	}
}

type shareForm struct {
	forms.Form
	Email  forms.TextField `json:"email"  validate:"trim required max_len:128 is_email"`
	Format forms.TextField `json:"format" validate:"trim format_choices"`
}

func (f *shareForm) GetTaggedValidator(name, _ string, tc *forms.TagContext) (forms.Validator, bool) {
	switch name {
	case "format_choices":
		forms.Choices(tc.Field,
			forms.Choice(forms.GetTranslator(tc.Context).Gettext("Article"), "html"),
			forms.Choice(forms.GetTranslator(tc.Context).Gettext("E-Book"), "epub"),
		)
		return nil, true
	default:
		return nil, false
	}
}

func (f *shareForm) sendBookmark(r *http.Request, b *bookmarks.Bookmark) (err error) {
	if !f.IsBound() {
		err = errors.New("form is not bound")
		return
	}

	var exporter converter.Exporter
	var options []email.MessageOption
	if u := auth.GetRequestUser(r); u != nil && u.Settings.EmailSettings.ReplyTo != "" {
		options = []email.MessageOption{
			func(_ context.Context, msg *email.Message) error {
				return msg.ReplyTo(u.Settings.EmailSettings.ReplyTo)
			},
		}
	}

	switch f.Format.Value() {
	case "html":
		exporter = converter.NewHTMLEmailExporter(
			f.Email.Value(),
			options...,
		)
	case "epub":
		exporter = converter.NewEPUBEmailExporter(
			f.Email.Value(),
			options...,
		)
	}

	if exporter == nil {
		err = errors.New("no exporter")
		f.AddErrors(forms.ErrUnexpected)
		return
	}

	if err = exporter.Export(
		r.Context(), nil, r,
		&dataset.BookmarkList{
			Count: 1,
			Items: []*dataset.Bookmark{
				dataset.NewBookmark(r.Context(), b),
			},
		},
	); err != nil {
		f.AddErrors(forms.ErrUnexpected)
		return
	}

	return
}
