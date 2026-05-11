// SPDX-FileCopyrightText: © 2020 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

// Package configs contains Readeck configuration.
package configs

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/araddon/dateparse"
	"github.com/caarlos0/env/v11"
	"github.com/komkom/toml"

	"codeberg.org/readeck/readeck/internal/acls"
)

var (
	version      = "dev"
	buildTimeStr string
	buildTime    time.Time
	startTime    = time.Now().UTC()

	trustedProxies     []*net.IPNet
	extractorDeniedIPs []*net.IPNet
)

func init() {
	buildTime, _ = dateparse.ParseAny(buildTimeStr)
}

// Because we don't need viper's mess for just storing configuration from
// a source.
type config struct {
	Main         configMain      `json:"main"`
	Server       configServer    `json:"server"`
	Database     configDB        `json:"database"`
	Auth         configAuth      `json:"auth"`
	Email        configEmail     `json:"email"`
	Accounts     configAccounts  `json:"accounts"`
	Extractor    configExtractor `json:"extractor"`
	Bookmarks    configBookmarks `json:"bookmarks"`
	Worker       configWorker    `json:"worker"`
	Metrics      configMetrics   `json:"metrics"`
	Customize    configCustomize `json:"customize"`
	Commissioned bool            `json:"-"`
}

type configMain struct {
	LogLevel      slog.Level `json:"log_level"       env:"LOG_LEVEL"`
	LogFormat     string     `json:"log_format"      env:"LOG_FORMAT"`
	LogTimeFormat string     `json:"log_time_format" env:"LOG_TIME_FORMAT"`
	SecretKey     string     `json:"secret_key"      env:"SECRET_KEY,unset"`
	DataDirectory string     `json:"data_directory"  env:"DATA_DIRECTORY,unset"`
}

type configServer struct {
	Host           string        `json:"host"            env:"SERVER_HOST"`
	Port           int           `json:"port"            env:"SERVER_PORT"`
	BaseURL        *configURL    `json:"base_url"        env:"SERVER_BASE_URL"`
	Prefix         string        `json:"prefix"          env:"SERVER_PREFIX"`
	TrustedProxies []configIPNet `json:"trusted_proxies" env:"TRUSTED_PROXIES,unset"`
	AllowedHosts   []string      `json:"allowed_hosts"   env:"ALLOWED_HOSTS"`
	CertFile       string        `json:"cert_file"       env:"CERT_FILE"`
	KeyFile        string        `json:"key_file"        env:"KEY_FILE"`
	ClientCAFile   string        `json:"client_ca_file"  env:"CLIENT_CA_FILE"`
	Session        configSession `json:"session"`
}

type configAuth struct {
	Forwarded configForwardedAuth
}

type configForwardedAuth struct {
	Enabled             bool   `json:"enabled"      env:"AUTH_FORWARDED_ENABLED"`
	ProvisioningEnabled bool   `json:"provisioning" env:"AUTH_FORWARDED_PROVISIONING"`
	HeaderUser          string `json:"header_user"  env:"AUTH_FORWARDED_HEADER_USER"`
	HeaderEmail         string `json:"header_email" env:"AUTH_FORWARDED_HEADER_EMAIL"`
	HeaderGroup         string `json:"header_group" env:"AUTH_FORWARDED_HEADER_GROUP"`
}

type configDB struct {
	Source string `json:"source" env:"DATABASE_SOURCE,unset"`
}

type configSession struct {
	CookieName string `json:"cookie_name"`
	MaxAge     int    `json:"max_age"` // in minutes
}

type configBookmarks struct {
	PublicShareTTL int `json:"public_share_ttl" env:"PUBLIC_SHARE_TTL"`
}

type configEmail struct {
	Debug       bool            `json:"debug"        env:"MAIL_DEBUG,unset"`
	Host        string          `json:"host"         env:"MAIL_HOST,unset"`
	Port        int             `json:"port"         env:"MAIL_PORT,unset"`
	Username    string          `json:"username"     env:"MAIL_USERNAME,unset"`
	Password    string          `json:"password"     env:"MAIL_PASSWORD,unset"`
	Encryption  string          `json:"encryption"   env:"MAIL_ENCRYPTION,unset"`
	Insecure    bool            `json:"insecure"     env:"MAIL_INSECURE,unset"`
	From        configEmailAddr `json:"from"         env:"MAIL_FROM,unset"`
	FromNoReply configEmailAddr `json:"from_noreply" env:"MAIL_FROMNOREPLY,unset"`
}

type configAccounts struct {
	UsernameDenyList []string `json:"username_denylist"`
	EmailDenyList    []string `json:"email_denylist"`
}

type configWorker struct {
	DSN         string `json:"dsn"          env:"WORKER_DSN,unset"`
	NumWorkers  int    `json:"num_workers"  env:"WORKER_NUMBER"`
	StartWorker bool   `json:"start_worker" env:"WORKER_START"`
}

type configExtractor struct {
	NumWorkers     int                `json:"workers"`
	ContentScripts []string           `json:"content_scripts"`
	DeniedIPs      []configIPNet      `json:"denied_ips"`
	ProxyMatch     []configProxyMatch `json:"proxy_match"`
}

type configMetrics struct {
	Host string `json:"host" env:"METRICS_HOST"`
	Port int    `json:"port" env:"METRICS_PORT"`
}

type configCustomize struct {
	ExtraPermissions []acls.Group `json:"extra_permissions"`
	ExtraTemplates   string       `json:"extra_templates"`
}

type configEmailAddr struct {
	*mail.Address
}

type configURL struct {
	*url.URL
}

type configIPNet struct {
	*net.IPNet
}

func (c *config) LoadFile(filename string) error {
	fd, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer fd.Close() //nolint:errcheck

	dec := json.NewDecoder(toml.New(fd))
	return dec.Decode(c)
}

func (c *config) LoadEnv() error {
	return env.ParseWithOptions(c, env.Options{
		Prefix:                "READECK_",
		UseFieldNameByDefault: false,
	})
}

func (c *config) validate() error {
	if len(map[string]struct{}{
		c.Auth.Forwarded.HeaderUser:  {},
		c.Auth.Forwarded.HeaderEmail: {},
		c.Auth.Forwarded.HeaderGroup: {},
	}) != 3 {
		return errors.New("auth.forwarded: all header names must be different")
	}

	return nil
}

func (a *configEmailAddr) parse(s string) (err error) {
	if strings.TrimSpace(s) == "" {
		a.Address = &mail.Address{}
	}
	a.Address, err = mail.ParseAddress(s)
	return err
}

func (a *configEmailAddr) setDefault() {
	if a.Address == nil || a.Address.Address == "" {
		a.Address = &mail.Address{Address: "unconfigured@localhost"}
	}
}

func (a *configEmailAddr) Addr() string {
	return a.Address.Address
}

// UnmarshalJSON implements [encoding.json.Unmarshaler].
func (a *configEmailAddr) UnmarshalJSON(d []byte) (err error) {
	var s string
	if err = json.Unmarshal(d, &s); err != nil {
		return err
	}
	return a.parse(s)
}

// UnmarshalText implements [encoding.TextUnmarshaler].
func (a *configEmailAddr) UnmarshalText(text []byte) (err error) {
	return a.parse(string(text))
}

// UnmarshalJSON implements [encoding.json.Unmarshaler].
func (cu *configURL) UnmarshalJSON(d []byte) (err error) {
	var s string
	if err = json.Unmarshal(d, &s); err != nil {
		return err
	}

	cu.URL, err = url.Parse(s)
	return err
}

// UnmarshalText implements [encoding.TextUnmarshaler].
func (cu *configURL) UnmarshalText(text []byte) (err error) {
	cu.URL, err = url.Parse(string(text))
	return err
}

func (cu *configURL) normalize() {
	cu.Fragment = ""
	cu.Path = path.Clean("/" + cu.Path)
	if !strings.HasSuffix(cu.Path, "/") {
		cu.Path += "/"
	}
}

// IsHTTP returns true when the URL has an http(s) scheme and a host.
func (cu *configURL) IsHTTP() bool {
	return (cu.Scheme == "http" || cu.Scheme == "https") && cu.Host != ""
}

func newConfigIPNet(v string) configIPNet {
	_, r, _ := net.ParseCIDR(v)
	return configIPNet{IPNet: r}
}

// parse loads a given string containing an ip address or
// a cidr. If it falls back to a single ip address, it gets a
// /32 or /128 netmask.
func (ci *configIPNet) parse(s string) error {
	// Try first to parse a cidr value
	_, r, err := net.ParseCIDR(s)
	if err == nil {
		ci.IPNet = r
		return nil
	}

	// If not cidr notation, then that's an ip with /32 or /128
	r = &net.IPNet{IP: net.ParseIP(s)}
	if r.IP.To4() != nil {
		r.Mask = net.CIDRMask(8*net.IPv4len, 8*net.IPv4len)
	} else {
		r.Mask = net.CIDRMask(8*net.IPv6len, 8*net.IPv6len)
	}
	ci.IPNet = r
	return nil
}

// UnmarshalJSON implements [encoding.json.Unmarshaler].
func (ci *configIPNet) UnmarshalJSON(d []byte) error {
	var s string
	err := json.Unmarshal(d, &s)
	if err != nil {
		return err
	}

	return ci.parse(s)
}

// UnmarshalText implements [encoding.TextUnmarshaler].
func (ci *configIPNet) UnmarshalText(text []byte) error {
	return ci.parse(string(text))
}

type configProxyMatch struct {
	host string
	url  *url.URL
}

func (pm *configProxyMatch) UnmarshalJSON(d []byte) error {
	var s map[string]string
	err := json.Unmarshal(d, &s)
	if err != nil {
		return err
	}

	if _, ok := s["host"]; !ok {
		return fmt.Errorf(`"host" not in %s`, d)
	}
	if _, ok := s["url"]; !ok {
		return fmt.Errorf(`"url" not in %s`, d)
	}

	proxy, err := url.Parse(s["url"])
	if err != nil {
		return fmt.Errorf("error with proxy URL %s in %s", s["url"], d)
	}

	pm.host = s["host"]
	pm.url = proxy

	return nil
}

func (pm configProxyMatch) Host() string {
	return pm.host
}

func (pm configProxyMatch) URL() *url.URL {
	return pm.url
}

// Config holds the configuration data from configuration files
// or flags.
//
// This variable sets some default values that might be overwritten
// by a configuration file.
var Config = config{
	Main: configMain{
		LogLevel:      slog.LevelInfo,
		DataDirectory: "data",
	},
	Server: configServer{
		Host: "127.0.0.1",
		Port: 8000,
		Session: configSession{
			CookieName: "sxid",
			MaxAge:     86400 * 30, // 30 days
		},
		TrustedProxies: []configIPNet{
			newConfigIPNet("127.0.0.0/8"),
			newConfigIPNet("10.0.0.0/8"),
			newConfigIPNet("172.16.0.0/12"),
			newConfigIPNet("192.168.0.0/16"),
			newConfigIPNet("fd00::/8"),
			newConfigIPNet("::1/128"),
		},
	},
	Database: configDB{},
	Email: configEmail{
		Port: 25,
	},
	Auth: configAuth{
		Forwarded: configForwardedAuth{
			Enabled:             false,
			HeaderUser:          "Remote-User",
			HeaderEmail:         "Remote-Email",
			HeaderGroup:         "Remote-Groups",
			ProvisioningEnabled: true,
		},
	},
	Bookmarks: configBookmarks{
		PublicShareTTL: 24,
	},
	Worker: configWorker{
		DSN:         "memory://",
		NumWorkers:  max(1, runtime.NumCPU()-1),
		StartWorker: true,
	},
	Extractor: configExtractor{
		NumWorkers: runtime.NumCPU(),
		DeniedIPs: []configIPNet{
			newConfigIPNet("127.0.0.0/8"),
			newConfigIPNet("::1/128"),
		},
		ProxyMatch: []configProxyMatch{},
	},
	Metrics: configMetrics{
		Host: "127.0.0.1",
		Port: 0,
	},
}

// LoadConfiguration loads the configuration file.
func LoadConfiguration(configPath string) error {
	if configPath == "" {
		return nil
	}

	if err := Config.LoadFile(configPath); err != nil {
		return err
	}

	// Override configuration from environment variables
	if err := Config.LoadEnv(); err != nil {
		return err
	}

	InitConfiguration()
	return Config.validate()
}

// InitConfiguration applies some default computed values on the configuration.
func InitConfiguration() {
	if Config.Database.Source == "" {
		Config.Database.Source = fmt.Sprintf("sqlite3:%s/db.sqlite3", Config.Main.DataDirectory)
	}

	if Config.Extractor.ContentScripts == nil {
		Config.Extractor.ContentScripts = []string{
			filepath.Join(Config.Main.DataDirectory, "content-scripts"),
		}
	}

	Config.Email.From.setDefault()
	Config.Email.FromNoReply.setDefault()

	if Config.Server.BaseURL != nil {
		Config.Server.BaseURL.normalize()
		if Config.Server.BaseURL.Path != "/" {
			Config.Server.Prefix = Config.Server.BaseURL.Path
		}
	}

	Config.Server.Prefix = path.Clean("/" + Config.Server.Prefix)
	if !strings.HasSuffix(Config.Server.Prefix, "/") {
		Config.Server.Prefix += "/"
	}

	// Load encryption and signing keys
	loadKeys()

	// Load the IP ranges
	trustedProxies = make([]*net.IPNet, 0, len(Config.Server.TrustedProxies))
	for _, x := range Config.Server.TrustedProxies {
		trustedProxies = append(trustedProxies, x.IPNet)
	}

	extractorDeniedIPs = make([]*net.IPNet, 0, len(Config.Extractor.DeniedIPs))
	for _, x := range Config.Extractor.DeniedIPs {
		extractorDeniedIPs = append(extractorDeniedIPs, x.IPNet)
	}
}

// TrustedProxies returns the value of Config.Server.TrustedProxies
// as a slice of [*net.IPNet].
func TrustedProxies() []*net.IPNet {
	return trustedProxies
}

// ExtractorDeniedIPs returns the value of Config.Extractor.DeniedIPs
// as a slice of [*net.IPNet].
func ExtractorDeniedIPs() []*net.IPNet {
	return extractorDeniedIPs
}

// Version returns the current readeck version.
func Version() string {
	return version
}

// BuildTime returns the build time or, if empty, the time
// when the application started.
func BuildTime() time.Time {
	if buildTime.IsZero() {
		return startTime
	}
	return buildTime
}
