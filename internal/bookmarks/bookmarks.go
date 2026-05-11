// SPDX-FileCopyrightText: © 2020 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package bookmarks provides storage and tooling
// for bookmarks and collections management.
package bookmarks

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/db"
	"codeberg.org/readeck/readeck/internal/db/exp"
	"codeberg.org/readeck/readeck/internal/db/types"
	"codeberg.org/readeck/readeck/pkg/base58"
)

// BookmarkState is the current bookmark state.
type BookmarkState int

const (
	// StateLoaded when the page is fully loaded.
	StateLoaded BookmarkState = iota

	// StateError when there was some unrecoverable
	// error during extraction.
	StateError

	// StateLoading when the page is loading.
	StateLoading
)

// StateNames returns a string with the state name.
var StateNames = map[BookmarkState]string{
	StateLoaded:  "loaded",
	StateError:   "error",
	StateLoading: "loading",
}

var (
	// Bookmarks is the bookmark query manager.
	Bookmarks = BookmarkManager{}

	// ErrBookmarkNotFound is returned when a bookmark record was not found.
	ErrBookmarkNotFound = errors.New("not found")
)

// StoragePath returns the storage base directory for bookmark files.
func StoragePath() string {
	return filepath.Join(configs.Config.Main.DataDirectory, "bookmarks")
}

// Bookmark is a bookmark record in database.
type Bookmark struct {
	ID            int                 `db:"id"            goqu:"skipinsert,skipupdate"`
	UID           string              `db:"uid"`
	UserID        *int                `db:"user_id"`
	Created       time.Time           `db:"created"       goqu:"skipupdate"`
	Updated       time.Time           `db:"updated"`
	State         BookmarkState       `db:"state"`
	URL           string              `db:"url"`
	InitialURL    string              `db:"initial_url"`
	Title         string              `db:"title"`
	Domain        string              `db:"domain"`
	Site          string              `db:"site"`
	SiteName      string              `db:"site_name"`
	Published     *time.Time          `db:"published"`
	Authors       types.Strings       `db:"authors"`
	Lang          string              `db:"lang"`
	TextDirection string              `db:"dir"`
	DocumentType  string              `db:"type"`
	Description   string              `db:"description"`
	Text          string              `db:"text"`
	WordCount     int                 `db:"word_count"`
	Duration      int                 `db:"duration"`
	Embed         string              `db:"embed"`
	FilePath      string              `db:"file_path"`
	Files         BookmarkFiles       `db:"files"`
	Errors        types.Strings       `db:"errors"`
	Labels        types.Strings       `db:"labels"`
	ReadProgress  int                 `db:"read_progress"`
	ReadAnchor    string              `db:"read_anchor"`
	IsArchived    bool                `db:"is_archived"`
	IsMarked      bool                `db:"is_marked"`
	Annotations   BookmarkAnnotations `db:"annotations"`
	Links         BookmarkLinks       `db:"links"`
}

// BookmarkManager is a query helper for bookmark entries.
type BookmarkManager struct{}

// Create inserts a new bookmark in the database.
func (m *BookmarkManager) Create(bookmark *Bookmark) error {
	if bookmark.UserID == nil {
		return errors.New("no bookmark user")
	}

	bookmark.UID = base58.NewUUID()
	bookmark.Updated = time.Now().UTC()
	if bookmark.Created.IsZero() {
		bookmark.Created = bookmark.Updated
	}

	if bookmark.InitialURL == "" {
		bookmark.InitialURL = bookmark.URL
	}

	ds := db.Q().Insert(db.TableBookmark).
		Rows(bookmark).
		Prepared(true)

	id, err := db.InsertWithID(ds, "id")
	if err != nil {
		return err
	}

	bookmark.ID = id
	return nil
}

// Query returns a prepared goqu SelectDataset that can be extended later.
func (m *BookmarkManager) Query() *goqu.SelectDataset {
	return db.Q().From(goqu.T(db.TableBookmark).As("b")).Prepared(true)
}

// GetOne executes the a select query and returns the first result or an error
// when there's no result.
func (m *BookmarkManager) GetOne(expressions ...goqu.Expression) (*Bookmark, error) {
	var b Bookmark
	found, err := m.Query().Where(expressions...).ScanStruct(&b)

	switch {
	case err != nil:
		return nil, err
	case !found:
		return nil, ErrBookmarkNotFound
	}

	return &b, nil
}

// DeleteUserBookmakrs remove all bookmarks for a given user.
// Normally we don't need such a process but since, a bookmark
// holds a file, we can't only rely on the foreign key cascade
// deletion. Hence this.
func (m *BookmarkManager) DeleteUserBookmakrs(u *users.User) error {
	ds := Bookmarks.Query().
		Where(goqu.C("user_id").Eq(u.ID))

	items := []*Bookmark{}
	if err := ds.ScanStructs(&items); err != nil {
		return err
	}

	for _, b := range items {
		if err := b.Delete(); err != nil {
			return err
		}
	}

	return nil
}

// GetLastUpdate returns the most recent "updated" value from a bookrmark set.
func (m *BookmarkManager) GetLastUpdate(expressions ...goqu.Expression) (time.Time, error) {
	var b Bookmark
	found, err := m.Query().
		Where(expressions...).
		Order(exp.DateTime(goqu.C("updated")).Desc()).
		Limit(1).
		ScanStruct(&b)

	switch {
	case err != nil:
		return time.Time{}, err
	case !found:
		return time.Time{}, nil
	}

	return b.Updated, nil
}

// GetLabels returns a dataset that returns all the tags
// defined in the bookmark table.
func (m *BookmarkManager) GetLabels() *goqu.SelectDataset {
	return exp.JSONStringsDataset(
		db.Q().
			From(goqu.T(db.TableBookmark).As("b")).
			Select(goqu.C("labels").Table("b")),
		"name",
	).
		SelectAppend(
			goqu.COUNT(goqu.C("id").Table("b")).As("count"),
		).
		GroupBy(goqu.C("name")).
		Prepared(true)
}

// GetAnnotations returns a SelectDataset that can be used to select all
// the annotations.
func (m *BookmarkManager) GetAnnotations() *goqu.SelectDataset {
	ds := Bookmarks.Query().Select(
		goqu.I("b.id").As(goqu.C("b.id")),
		goqu.I("b.uid").As(goqu.C("b.uid")),
		goqu.I("b.url").As(goqu.C("b.url")),
		goqu.I("b.title").As(goqu.C("b.title")),
		goqu.I("b.site_name").As(goqu.C("b.site_name")),
	)
	switch db.Driver().Dialect() {
	case "postgres":
		ds = ds.SelectAppend(
			goqu.L(`a->>'id'`).As("annotation_id"),
			goqu.L(`a->>'text'`).As("annotation_text"),
			goqu.L(`(a->>'created')::timestamptz`).As("annotation_created"),
			goqu.L(`COALESCE((a->>'color'), 'yellow')`).As("annotation_color"),
			goqu.L(`COALESCE((a->>'note'), '')`).As("annotation_note"),
		).
			From(
				goqu.T(db.TableBookmark).As("b"),
				goqu.L(`jsonb_array_elements(
					case jsonb_typeof(b.annotations)
					when 'array' then b.annotations
					else '[]' end
					)`).As("a"),
			)
	case "sqlite3":
		ds = ds.SelectAppend(
			goqu.Func("json_extract", goqu.I("a.value"), "$.id").As("annotation_id"),
			goqu.Func("json_extract", goqu.I("a.value"), "$.text").As("annotation_text"),
			goqu.Func("json_extract", goqu.I("a.value"), "$.created").As("annotation_created"),
			goqu.Func("COALESCE", goqu.Func("json_extract", goqu.I("a.value"), "$.color"), "yellow").As("annotation_color"),
			goqu.Func("COALESCE", goqu.Func("json_extract", goqu.I("a.value"), "$.note"), "").As("annotation_note"),
		).
			From(
				goqu.T(db.TableBookmark).As("b"),
				goqu.L(`json_each(
					case json_type(b.annotations)
					when 'array' then b.annotations
					else '[]' end
				)`).As("a"),
			).
			Where(
				goqu.Func("json_valid", goqu.I("b.annotations")),
			)
	}

	ds = ds.Prepared(true)
	return ds
}

type countQueryResult struct {
	Count      int    `db:"count"`
	IsArchived bool   `db:"is_archived"`
	IsMarked   bool   `db:"is_marked"`
	Type       string `db:"type"`
}

// CountResult contains the result of the total bookmark count with marked, archived and types
// breakdown.
type CountResult struct {
	Total    int
	Unread   int
	Archived int
	Marked   int
	ByType   map[string]int
}

// CountAll returns a CountResult of all bookmarks for a given user.
func (m *BookmarkManager) CountAll(u *users.User) (CountResult, error) {
	ds := Bookmarks.Query().
		Select(
			goqu.COUNT(goqu.C("id")).As("count"),
			goqu.C("is_marked"),
			goqu.C("is_archived"),
			goqu.C("type"),
		).
		Where(goqu.C("user_id").Eq(u.ID)).
		GroupBy(
			goqu.C("is_marked"),
			goqu.C("is_archived"),
			goqu.C("type"),
		)

	res := CountResult{ByType: map[string]int{}}
	items := []*countQueryResult{}
	if err := ds.ScanStructs(&items); err != nil {
		return res, err
	}

	for _, x := range items {
		res.Total += x.Count
		if x.IsArchived {
			res.Archived += x.Count
		}
		if x.IsMarked {
			res.Marked += x.Count
		}
		if !x.IsArchived {
			res.Unread += x.Count
		}
		if x.Type != "" {
			res.ByType[x.Type] += x.Count
		}
	}
	return res, nil
}

// RenameLabel renames or deletes a label in all bookmarks for a given user.
// If "newLabel" is empty, the label is deleted.
func (m *BookmarkManager) RenameLabel(u *users.User, oldLabel, newLabel string) (ids []int, err error) {
	ids = make([]int, 0)

	ds := Bookmarks.Query().
		Select("b.id", "b.labels").
		Where(goqu.C("user_id").Eq(u.ID))
	ds = exp.JSONListFilter(ds, goqu.I("b.labels").Eq(oldLabel))

	list := []*Bookmark{}
	if err = ds.ScanStructs(&list); err != nil {
		return
	}

	if len(list) == 0 {
		return
	}

	ids = make([]int, len(list))
	cases := goqu.Case()
	casePlaceholder := "?"
	if db.Driver().Dialect() == "postgres" {
		casePlaceholder = "?::jsonb"
	}

	for i, x := range list {
		ids[i] = x.ID
		x.replaceLabel(oldLabel, newLabel)
		cases = cases.When(goqu.C("id").Eq(x.ID), goqu.L(casePlaceholder, x.Labels))
	}

	_, err = db.Q().Update(db.TableBookmark).Prepared(true).
		Set(goqu.Record{
			"updated": time.Now().UTC(),
			"labels":  cases,
		}).
		Where(goqu.C("id").In(ids)).
		Executor().Exec()
	if err != nil {
		return nil, err
	}

	return
}

// DiskUsage returns the total size of bookmarks on disk, as int64.
func (m *BookmarkManager) DiskUsage(fn func(uid string) bool) (uint64, error) {
	dir := StoragePath()
	var totalSize uint64

	if _, err := os.Lstat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}

	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		ok := true
		if fn != nil {
			ok = fn(strings.Split(info.Name(), ".")[0])
		}

		if !info.IsDir() && ok {
			totalSize += uint64(info.Size())
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	return totalSize, nil
}

// UserDiskUsage returns the total size of bookmarks on disk for a given user.
// The first (count) result is the number of bookmarks.
func (m *BookmarkManager) UserDiskUsage(user *users.User) (count int, size uint64, err error) {
	uids := map[string]struct{}{}
	rows, err := Bookmarks.Query().
		Select(goqu.C("uid").Table("b")).
		Where(goqu.C("user_id").Table("b").Eq(user.ID)).
		Executor().Query()
	if err != nil {
		return count, size, err
	}

	s := ""
	for rows.Next() {
		if err = rows.Scan(&s); err != nil {
			return count, size, err
		}
		uids[s] = struct{}{}
	}
	if err = rows.Close(); err != nil {
		return count, size, err
	}

	size, err = m.DiskUsage(func(uid string) bool {
		_, ok := uids[uid]
		return ok
	})
	if err != nil {
		return count, size, err
	}

	count = len(uids)
	return count, size, nil
}

// Update updates some bookmark values.
func (b *Bookmark) Update(v any) error {
	if b.ID == 0 {
		return errors.New("No ID")
	}

	switch v := v.(type) {
	case map[string]any:
		v["updated"] = time.Now().UTC()
	default:
		//
	}

	_, err := db.Q().Update(db.TableBookmark).Prepared(true).
		Set(v).
		Where(goqu.C("id").Eq(b.ID)).
		Executor().Exec()

	return err
}

// Save updates all the bookmark values.
func (b *Bookmark) Save() error {
	b.Updated = time.Now().UTC()
	return b.Update(b)
}

// Delete removes a bookmark from the database.
func (b *Bookmark) Delete() error {
	_, err := db.Q().Delete(db.TableBookmark).Prepared(true).
		Where(goqu.C("id").Eq(b.ID)).
		Executor().Exec()
	if err != nil {
		return err
	}

	b.RemoveFiles()
	return nil
}

// StateName returns the current bookmark state name.
func (b *Bookmark) StateName() string {
	return StateNames[b.State]
}

// ReadingTime returns the duration or the aproximated reading time.
func (b *Bookmark) ReadingTime() int {
	if b.Duration > 0 {
		return b.Duration / 60
	}
	return b.WordCount / 200
}

// GetBaseFileURL returns the base path for archive URL.
func (b *Bookmark) GetBaseFileURL() (string, error) {
	return path.Join(b.UID[:2], b.UID), nil
}

// RemoveFiles removes all the bookmark's files.
func (b *Bookmark) RemoveFiles() {
	filename := b.GetFilePath()
	if filename == "" {
		return
	}

	l := slog.With(slog.String("path", filename))
	if err := os.Remove(filename); err != nil {
		l.Error("", slog.Any("err", err))
	} else {
		l.Debug("file removed")
	}

	// Remove empty directories up to the base
	dirname := path.Dir(filename)
	if stat, _ := os.Stat(dirname); stat == nil {
		return
	}
	for dirname != "." {
		// Just try to remove and if it's not empty it will complain
		d := dirname
		if err := os.Remove(d); err != nil {
			break
		}
		slog.Debug("directory removed", slog.String("dir", dirname))
		dirname = path.Dir(dirname)
	}
}

// GetFilePath returns the bookmark's associated file path.
func (b *Bookmark) GetFilePath() string {
	if b.FilePath == "" {
		return ""
	}
	return filepath.Join(StoragePath(), b.FilePath+".zip")
}

// replaceLabel replaces "old" label with "new" in the
// bookmark's Labels. It does not save the bookmark into
// the database.
// If new is empty, the label is removed from the list.
func (b *Bookmark) replaceLabel(oldLabel, newLabel string) {
	if b.Labels == nil {
		return
	}

	if strings.TrimSpace(newLabel) == "" {
		b.Labels = slices.DeleteFunc(slices.Clone(b.Labels), func(s string) bool {
			return s == oldLabel
		})
	} else {
		for i, v := range b.Labels {
			if v == oldLabel {
				b.Labels[i] = newLabel
			}
		}
	}

	slices.SortFunc(b.Labels, db.UnaccentCompare)
	b.Labels = slices.Compact(b.Labels)
}

// UpdateEtag returns the string used to generate the etag
// of the bookmark(s).
func (b *Bookmark) UpdateEtag(h hash.Hash) {
	io.WriteString(h, b.UID+strconv.FormatInt(b.Updated.UTC().UnixNano(), 10))
}

// GetLastModified returns the last modified times.
func (b *Bookmark) GetLastModified() []time.Time {
	return []time.Time{b.Updated}
}

// BookmarkLinks is a list of BookmarkLink instances.
type BookmarkLinks []BookmarkLink

// BookmarkLink describes a link.
type BookmarkLink struct {
	URL         string `json:"url"`
	Domain      string `json:"domain"`
	Title       string `json:"title"`
	IsPage      bool   `json:"is_page"`
	ContentType string `json:"content_type"`
}

// Scan loads a BookmarkLinks instance from a column.
func (l *BookmarkLinks) Scan(value any) error {
	if value == nil {
		return nil
	}

	v, err := types.JSONBytes(value)
	if err != nil {
		return err
	}
	json.Unmarshal(v, l) //nolint:errcheck
	return nil
}

// Value encodes a BookmarkLinks instance for storage.
func (l BookmarkLinks) Value() (driver.Value, error) {
	v, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	return string(v), nil
}

// HasPages returns true if the link list contains at least one
// link that refers to an HTML page.
func (l BookmarkLinks) HasPages() bool {
	return len(l) > 0 && slices.ContainsFunc(l, func(bl BookmarkLink) bool {
		return bl.IsPage
	})
}

// Pages returns a list of pages only.
func (l BookmarkLinks) Pages() BookmarkLinks {
	return slices.DeleteFunc(slices.Clone(l), func(bl BookmarkLink) bool {
		return !bl.IsPage
	})
}

// BookmarkFiles is a map of BookmarkFile instances.
type BookmarkFiles map[string]*BookmarkFile

// BookmarkFile represents a stored file (attachment) for a bookmark.
// The Size property is ony useful for images.
type BookmarkFile struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Size [2]int `json:"size,omitempty"`
}

// Scan loads a BookmarkFiles instance from a column.
func (f *BookmarkFiles) Scan(value any) error {
	if value == nil {
		return nil
	}

	v, err := types.JSONBytes(value)
	if err != nil {
		return err
	}
	json.Unmarshal(v, f) //nolint:errcheck
	return nil
}

// Value encodes a BookmarkFiles instance for storage.
func (f BookmarkFiles) Value() (driver.Value, error) {
	v, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	return string(v), nil
}

// AnnotationQueryResult hold the content of an annotation.
type AnnotationQueryResult struct {
	Bookmark Bookmark         `db:"b"`
	ID       string           `db:"annotation_id"`
	Text     string           `db:"annotation_text"`
	Created  types.TimeString `db:"annotation_created"`
	Color    string           `db:"annotation_color"`
	Note     string           `db:"annotation_note"`
}
