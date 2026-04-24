// SPDX-FileCopyrightText: © 2025 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package testing provides tools to tests the HTTP routes, the message bus, email sending, etc.
package testing

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/kinbiko/jsonassert"
	"github.com/stretchr/testify/require"

	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/app"
	"codeberg.org/readeck/readeck/internal/auth/tokens"
	"codeberg.org/readeck/readeck/internal/auth/users"
	"codeberg.org/readeck/readeck/internal/bookmarks"
	"codeberg.org/readeck/readeck/internal/db"
	"codeberg.org/readeck/readeck/internal/email"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/sessions"
	"codeberg.org/readeck/readeck/pkg/superbus"
)

type fixtureData struct {
	Users map[string]struct {
		Group     string `json:"group"`
		Bookmarks []struct {
			UID    string                  `json:"uid"`
			URL    string                  `json:"url"`
			State  bookmarks.BookmarkState `json:"state"`
			Labels []string                `json:"labels"`
		} `json:"bookmarks"`
	}
	Files map[string]string `json:"files"`

	users map[string]*TestUser
}

func loadFixtures(t *testing.T) *fixtureData {
	_, curFile, _, _ := runtime.Caller(0)
	fd, err := os.Open(path.Join(path.Dir(curFile), "fixtures/data.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close() // nolint:errcheck

	dec := json.NewDecoder(fd)
	res := new(fixtureData)
	if err := dec.Decode(res); err != nil {
		t.Fatal(err)
	}

	res.copyFiles(t)
	res.createUsers(t)
	res.createBookmarks(t)

	return res
}

func (f *fixtureData) createUsers(t *testing.T) {
	f.users = map[string]*TestUser{}
	for name, user := range f.Users {
		if name == "" {
			f.users[name] = &TestUser{}
			continue
		}

		tu, err := NewTestUser(name, name+"@localhost", name, user.Group)
		if err != nil {
			t.Fatal(err)
		}
		f.users[name] = tu
		t.Logf("created user: %s[%s]", tu.User.Username, tu.User.Group)
	}
}

func (f *fixtureData) copyFiles(t *testing.T) {
	_, curFile, _, _ := runtime.Caller(0)
	root := path.Join(path.Dir(curFile), "fixtures")

	for dstFile, srcFile := range f.Files {
		func(dstFile, srcFile string) {
			dstFile = path.Join(configs.Config.Main.DataDirectory, dstFile)
			srcFile = path.Join(root, srcFile)
			if err := os.MkdirAll(path.Dir(dstFile), 0o750); err != nil {
				t.Fatal(err)
			}

			src, err := os.Open(srcFile)
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close() // nolint:errcheck

			dst, err := os.Create(dstFile)
			if err != nil {
				t.Fatal(err)
			}
			defer dst.Close() // nolint:errcheck

			if _, err := io.Copy(dst, src); err != nil {
				t.Fatal(err)
			}

			t.Logf("copy %s -> %s", srcFile, dstFile)
		}(dstFile, srcFile)
	}
}

func (f *fixtureData) createBookmarks(t *testing.T) {
	for username, tu := range f.users {
		tu.Bookmarks = []*bookmarks.Bookmark{}
		for _, bookmark := range f.Users[username].Bookmarks {
			b := &bookmarks.Bookmark{
				URL:      bookmark.URL,
				State:    bookmark.State,
				FilePath: bookmark.UID[0:2] + "/" + bookmark.UID,
				Labels:   bookmark.Labels,
			}
			if username == "" {
				b.UID = bookmark.UID
				tu.Bookmarks = append(tu.Bookmarks, b)
				continue
			}

			b.UserID = &tu.User.ID

			if err := bookmarks.Bookmarks.Create(b); err != nil {
				t.Fatal(err)
			}
			b.UID = bookmark.UID
			if err := b.Save(); err != nil {
				t.Fatal(err)
			}
			tu.Bookmarks = append(tu.Bookmarks, b)
		}
	}
}

// TestUser contains the user data that we can use during tests.
type TestUser struct {
	User      *users.User
	Token     *tokens.Token
	Bookmarks []*bookmarks.Bookmark
	password  string
	token     string
}

// NewTestUser creates a new user for testing.
func NewTestUser(name, email, password, group string) (*TestUser, error) {
	u := &users.User{
		Username: name,
		Email:    email,
		Password: password,
		Group:    group,
		Settings: &users.UserSettings{
			Lang: "en",
		},
	}
	if err := users.Users.Create(u); err != nil {
		return nil, err
	}

	res := &TestUser{
		User:      u,
		password:  password,
		Bookmarks: []*bookmarks.Bookmark{},
	}

	res.Token = &tokens.Token{
		UserID:      &u.ID,
		IsEnabled:   true,
		Application: "tests",
	}
	if err := tokens.Tokens.Create(res.Token); err != nil {
		return nil, err
	}
	token, err := configs.Keys.TokenKey().Encode(res.Token.UID)
	if err != nil {
		return nil, err
	}
	res.token = token

	return res, nil
}

// Reset sets the user password and generate a new seed.
// It needs to be called on teardown after tests that could
// change the seed and/or password.
func (tu *TestUser) Reset() error {
	if err := tu.User.SetPassword(tu.password); err != nil {
		return err
	}
	tu.User.SetSeed()
	return tu.User.Save()
}

// APIToken returns the user's API token.
func (tu *TestUser) APIToken() string {
	return tu.token
}

func (tu *TestUser) sessionCookie() *http.Cookie {
	// Create and encoded a session cookie
	encoded, err := server.SessionHandler().Encode(&sessions.Payload{
		Seed:        tu.User.Seed,
		User:        tu.User.ID,
		LastUpdate:  time.Now().UTC(),
		Flashes:     []sessions.FlashMessage{},
		Preferences: sessions.Preferences{},
	})
	if err != nil {
		panic(err)
	}

	return &http.Cookie{
		Name:     configs.Config.Server.Session.CookieName,
		Path:     "/",
		MaxAge:   configs.Config.Server.Session.MaxAge,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().UTC().Add(time.Duration(configs.Config.Server.Session.MaxAge) * time.Second),
		Value:    base64.URLEncoding.EncodeToString(encoded),
	}
}

// TestApp holds information of the application for testing.
type TestApp struct {
	TmpDir    string
	Srv       *server.Server
	Users     map[string]*TestUser
	Bookmarks map[string]*bookmarks.Bookmark
	LastEmail string
}

// NewTestApp initializes TestApp with a default configuration,
// some users, and an http muxer ready to accept requests.
func NewTestApp(t *testing.T) *TestApp {
	var err error
	tmpDir := t.TempDir()

	configs.Config.Main.SecretKey = "1234567890"
	configs.Config.Main.DataDirectory = tmpDir
	configs.Config.Extractor.ContentScripts = []string{path.Join(tmpDir, "content-scripts")}
	configs.Config.Main.LogLevel = slog.LevelError
	configs.Config.Database.Source = "sqlite3::memory:"
	configs.Config.Server.AllowedHosts = []string{"readeck.example.org"}

	configs.InitConfiguration()

	// Init test app
	ta := &TestApp{
		TmpDir:    tmpDir,
		Users:     map[string]*TestUser{},
		Bookmarks: map[string]*bookmarks.Bookmark{},
	}

	// Email sender before init app
	configs.Config.Email.Host = "localhost"
	email.InitSender()
	email.Sender = ta

	// Init application
	app.InitApp()
	configs.Config.Commissioned = true

	// Load data
	fixtures := loadFixtures(t)
	ta.Users = fixtures.users

	// Start event manager
	startEventManager()

	// Init test server
	ta.Srv = server.New()
	err = app.InitServer(ta.Srv)
	if err != nil {
		t.Fatal(err)
	}

	return ta
}

// Close removes artifacts that were needed for testing.
func (ta *TestApp) Close(t *testing.T) {
	if err := db.Close(); err != nil {
		t.Logf("error closing database: %s", err)
	}
	if err := os.RemoveAll(ta.TmpDir); err != nil {
		t.Logf("error removing temporary folder: %s", err)
	}

	t.Logf("removed folder: %s", ta.TmpDir)

	// Reset the bus
	Events().Stop()
	Store().Clear()
}

// Client creates a new [Client] instance.
func (ta *TestApp) Client(options ...ClientOption) *Client {
	jar, _ := cookiejar.New(nil)
	c := &Client{
		app:     ta,
		URL:     &url.URL{Scheme: "https", Host: "readeck.example.org"},
		Jar:     jar,
		Header:  http.Header{},
		History: []HistoryItem{},
	}

	for _, f := range options {
		f(c)
	}

	return c
}

// SendEmail implements [email.sender] interface and stores the last sent message.
func (ta *TestApp) SendEmail(msg *email.Message) error {
	buf := new(bytes.Buffer)
	msg.WriteTo(buf) // nolint:errcheck
	ta.LastEmail = buf.String()
	return nil
}

// HistoryItem is a client's history item.
type HistoryItem struct {
	URL      *url.URL
	Request  *http.Request
	Response *Response
}

// ClientHistory is a list of [HistoryItem].
type ClientHistory []HistoryItem

// PrevURL returns the URL from the first history item.
func (h ClientHistory) PrevURL() string {
	return h[0].URL.String()
}

// ClientOption is a function passed to [TestApp.Client].
type ClientOption func(c *Client)

// WithSession adds a session cookies to the client.
func WithSession(username string) ClientOption {
	return func(c *Client) {
		u, ok := c.app.Users[username]
		if !ok || u.User == nil {
			return
		}

		c.Jar.SetCookies(c.URL, []*http.Cookie{u.sessionCookie()})
	}
}

// WithToken adds an Authorization header with the user's token to the client.
func WithToken(username string) ClientOption {
	return func(c *Client) {
		c.Header.Set("Accept", "application/json")

		u, ok := c.app.Users[username]
		if !ok || u.Token == nil {
			return
		}

		c.Header.Set("Authorization", "Bearer "+u.APIToken())
	}
}

// Client is a thin HTTP client over the main server router.
type Client struct {
	app     *TestApp
	URL     *url.URL
	Jar     http.CookieJar
	Header  http.Header
	History ClientHistory
}

// NewRequest creates a new [http.Request].
//
// body of types [io.Reader], []byte, string or nil are passed as is.
//
// When the body is of type [url.Values], the request's
// Content-Type is set to "application/x-www-form-urlencoded".
//
// Otherwise, the body is marshaled and the Content-Type is set to "application/json".
func (c *Client) NewRequest(method, target string, body any) (*http.Request, error) {
	header := http.Header{}
	maps.Copy(header, c.Header)

	var b io.Reader

	switch t := body.(type) {
	case io.Reader:
		b = t
	case []byte:
		b = bytes.NewReader(t)
	case string:
		b = strings.NewReader(t)
	case url.Values:
		b = strings.NewReader(t.Encode())
		header.Set("Content-Type", "application/x-www-form-urlencoded")
	case nil:
		b = nil
	default:
		b = new(bytes.Buffer)
		if err := json.NewEncoder(b.(io.Writer)).Encode(t); err != nil {
			return nil, err
		}
		header.Set("Content-Type", "application/json")
	}

	req := httptest.NewRequest(method, target, b)
	req.URL.Host = c.URL.Host
	req.URL.Scheme = c.URL.Scheme
	req.Host = c.URL.Host

	for _, cookie := range c.Jar.Cookies(req.URL) {
		req.AddCookie(cookie)
	}

	maps.Copy(req.Header, header)

	return req, nil
}

// Request performs a Request using httptest tools.
// It returns a Response instance that can be evaluated for testing
// purposes.
func (c *Client) Request(t *testing.T, req *http.Request) *Response {
	w := httptest.NewRecorder()

	// Perform request
	c.app.Srv.ServeHTTP(w, req)

	// Update cookies from response
	if rc := w.Result().Cookies(); len(rc) > 0 {
		c.Jar.SetCookies(req.URL, rc)
	}

	// Prepare response instance
	rsp, err := NewResponse(w, req)
	rsp.Request = req
	if err != nil {
		t.Fatal(err)
	}

	item := HistoryItem{
		URL:      new(url.URL),
		Request:  req,
		Response: rsp,
	}
	*item.URL = *req.URL
	item.URL.Scheme = ""
	item.URL.Host = ""

	c.History = append(ClientHistory{item}, c.History...)

	return rsp
}

// RT prepares a [RequestTest] and returns a function that receives a [testing.RT]
// variable, runs the request and performs the assertions.
func (c *Client) RT(t *testing.T, options ...TestOption) {
	c.Run(t, RT(options...))
}

// Assert runs the [RequestTest] and performs the [RequestTest.Assert] functions.
func (c *Client) Assert(t *testing.T, rt *RequestTest) {
	req, err := c.NewRequest(rt.Method, rt.Target, rt.Body)
	if err != nil {
		t.Fatal(err)
	}
	maps.Copy(req.Header, rt.Header)
	rsp := c.Request(t, req)
	for _, f := range rt.Assert {
		f(t, rsp)
	}
}

// Run runs the request from [RequestTest] and performs
// the assertions.
func (c *Client) Run(t *testing.T, rt *RequestTest) bool {
	return t.Run(rt.Name, func(t *testing.T) {
		t.Attr("route", rt.Method+" "+rt.Target)
		c.Assert(t, rt)
	})
}

// Sequence returns a function that receives a [testing.T] variable and runs
// the given [RequestTest] list.
func (c *Client) Sequence(t *testing.T, tests ...*RequestTest) {
	for _, rt := range tests {
		c.Run(t, rt)
	}
}

type (
	// TestOption is an option for [RequestTest].
	TestOption func(rt *RequestTest)

	// RspAssertion is a [Response] assertion function.
	RspAssertion func(t *testing.T, rsp *Response)

	// RequestTest contains data that are used to perform requests.
	RequestTest struct {
		Name   string
		Method string
		Target string
		Body   any
		Header http.Header
		Assert []RspAssertion
	}
)

// RT creates a new [RequestTest].
func RT(options ...TestOption) *RequestTest {
	rt := &RequestTest{
		Method: http.MethodGet,
		Header: http.Header{},
	}

	for _, f := range options {
		f(rt)
	}

	if rt.Name == "" {
		rt.Name = rt.Method + "_" + strings.ReplaceAll(strings.Trim(rt.Target, "/"), "/", "_")
	}

	return rt
}

// WithName sets the [RequestTest.Name].
func WithName(name string) TestOption {
	return func(rt *RequestTest) {
		rt.Name = name
	}
}

// WithMethod sets the [RequestTest.Method].
func WithMethod(method string) TestOption {
	return func(rt *RequestTest) {
		rt.Method = method
	}
}

// WithTarget sets the [RequestTest.Target].
func WithTarget(target string) TestOption {
	return func(rt *RequestTest) {
		rt.Target = target
	}
}

// WithBody sets the [RequestTest.Body].
func WithBody(body any) TestOption {
	return func(rt *RequestTest) {
		rt.Body = body
	}
}

// WithHeader adds a value to [RequestTest.Header].
func WithHeader(name, value string) TestOption {
	return func(rt *RequestTest) {
		rt.Header.Add(name, value)
	}
}

// WithAssert adds an [RspAssertion] to the [RequestTest.Assert].
func WithAssert(assertion RspAssertion) TestOption {
	return func(rt *RequestTest) {
		rt.Assert = append(rt.Assert, assertion)
	}
}

// AssertStatus checks the response's expected status.
func AssertStatus(status int) TestOption {
	return WithAssert(func(t *testing.T, rsp *Response) {
		rsp.AssertStatus(t, status)
	})
}

// AssertRedirect checks that the expected target is present in a Location header.
func AssertRedirect(target string) func(rt *RequestTest) {
	return WithAssert(func(t *testing.T, rsp *Response) {
		rsp.AssertRedirect(t, target)
	})
}

// AssertContains checks that the response's body contains the expected string.
func AssertContains(expected string) TestOption {
	return WithAssert(func(t *testing.T, rsp *Response) {
		rsp.AssertContains(t, expected)
	})
}

// AssertJSON checks that the response's JSON matches what we expect.
func AssertJSON(expected string) TestOption {
	return WithAssert(func(t *testing.T, rsp *Response) {
		rsp.AssertJSON(t, expected)
	})
}

// Response is a wrapper around http.Response where the body is stored and
// the HTML (when applicable) is parsed in advance.
type Response struct {
	*http.Response
	URL      *url.URL
	Redirect string
	Body     []byte
	HTML     *html.Node
	JSON     any
}

// NewResponse returns a Response instance based on the ResponseRecorder
// given in input.
func NewResponse(rec *httptest.ResponseRecorder, req *http.Request) (*Response, error) {
	var err error
	r := &Response{Response: rec.Result()} //nolint:bodyclose

	u2 := new(url.URL)
	*u2 = *req.URL
	u2.Scheme = "http"

	r.URL = u2

	// Set redirect if any
	if loc := r.Header.Get("location"); loc != "" {
		redir, err := r.URL.Parse(loc)
		if err != nil {
			return nil, err
		}
		if redir.Host == u2.Host {
			redir.Scheme = ""
			redir.Host = ""
		}
		r.Redirect = redir.String()
	}

	// Read the response's body
	r.Body, err = io.ReadAll(r.Response.Body)
	if err != nil {
		return nil, err
	}

	// When an HTML response is received, parse it
	switch {
	case strings.HasPrefix(r.Header.Get("content-type"), "text/html"):
		r.HTML, err = html.Parse(bytes.NewReader(r.Body))
		if err != nil {
			return nil, err
		}
	case strings.HasPrefix(r.Header.Get("content-type"), "application/json"):
		err := json.Unmarshal(r.Body, &r.JSON)
		if err != nil {
			return nil, err
		}
	}

	return r, nil
}

// Path returns the path and querystring of the response URL.
func (r *Response) Path() string {
	u := new(url.URL)
	*u = *r.URL
	u.Scheme = ""
	u.Host = ""
	return u.String()
}

// AssertStatus checks the response's expected status.
func (r *Response) AssertStatus(t *testing.T, expected int) {
	require.Equal(t, expected, r.StatusCode)
}

// AssertRedirect checks that the expected target is present in a Location header.
func (r *Response) AssertRedirect(t *testing.T, expected string) {
	require.Regexp(t, expected, r.Redirect)
}

// AssertContains checks that the response's body contains the expected string.
func (r *Response) AssertContains(t *testing.T, expected string) {
	require.Contains(t, string(r.Body), expected)
}

// AssertJSON checks that the response's JSON matches what we expect.
func (r *Response) AssertJSON(t *testing.T, expected string) {
	jsonassert.New(t).Assertf(string(r.Body), "%s", expected)
	if t.Failed() {
		t.Errorf("Received JSON: %s\n", string(r.Body))
		t.FailNow()
	}
}

// GetTaskPayload returns a decoded task payload.
func GetTaskPayload[T any](t *testing.T, name string, task superbus.Task) T {
	data := Store().Get(name)
	if data == nil {
		t.Fatal("empty task data")
	}

	var p superbus.Payload
	if err := superbus.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}

	res, ok := task.Unmarshal(p.Data).(T)
	if !ok {
		t.Fatal("invalid payload type")
	}
	return res
}
