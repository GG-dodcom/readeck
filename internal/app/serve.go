// SPDX-FileCopyrightText: © 2020 Olivier Meunier <olivier@neokraft.net>
//
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cristalhq/acmd"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"codeberg.org/readeck/readeck/components"
	"codeberg.org/readeck/readeck/configs"
	"codeberg.org/readeck/readeck/internal/admin"
	"codeberg.org/readeck/readeck/internal/assets"
	"codeberg.org/readeck/readeck/internal/auth/oauth2"
	"codeberg.org/readeck/readeck/internal/auth/onboarding"
	"codeberg.org/readeck/readeck/internal/auth/signin"
	bookmark_routes "codeberg.org/readeck/readeck/internal/bookmarks/routes"
	"codeberg.org/readeck/readeck/internal/bus"
	"codeberg.org/readeck/readeck/internal/cookbook"
	"codeberg.org/readeck/readeck/internal/dashboard"
	"codeberg.org/readeck/readeck/internal/docs"
	"codeberg.org/readeck/readeck/internal/metrics"
	"codeberg.org/readeck/readeck/internal/opds"
	"codeberg.org/readeck/readeck/internal/profile"
	"codeberg.org/readeck/readeck/internal/server"
	"codeberg.org/readeck/readeck/internal/server/urls"
	"codeberg.org/readeck/readeck/internal/videoplayer"
)

type serveFlags struct {
	appFlags
	Host string
	Port uint
}

func (f *serveFlags) Flags() *flag.FlagSet {
	fs := f.appFlags.Flags()
	fs.StringVar(&f.Host, "host", "", "Listen to address")
	fs.UintVar(&f.Port, "port", 0, "Listen to port")

	return fs
}

func init() {
	commands = append(commands, acmd.Command{
		Name:        "serve",
		Description: "Start Readeck HTTP server",
		ExecFunc:    runServer,
	})
}

func runServer(_ context.Context, args []string) error { // nolint:gocognit,gocyclo
	var flags serveFlags
	if err := flags.Flags().Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	// Init application
	if err := appPreRun(&flags.appFlags); err != nil {
		return err
	}
	defer appPostRun()

	// Command flags are the last override values
	if flags.Host != "" {
		configs.Config.Server.Host = flags.Host
	}
	if flags.Port > 0 {
		configs.Config.Server.Port = int(flags.Port)
	}

	// Prepare HTTP server
	s := server.New()
	if err := InitServer(s); err != nil {
		return err
	}

	if err := bus.Load(); err != nil {
		return err
	}

	ready := make(chan bool)
	stop := make(chan os.Signal, 2)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Start the metrics HTTP server
	startMetrics := configs.Config.Metrics.Port > 0
	if startMetrics {
		slog.Info("metrics endpoint",
			slog.String("host", configs.Config.Metrics.Host),
			slog.Int("port", configs.Config.Metrics.Port),
		)
		go func() {
			if err := metrics.ListenAndServe(
				configs.Config.Metrics.Host,
				configs.Config.Metrics.Port,
			); err != nil {
				fatal("cannot start the metrics server", err)
			}
		}()
	}

	// Start the embed standalone worker.
	startBus := configs.Config.Worker.StartWorker || bus.Protocol() == "memory"
	if startBus {
		go func() {
			bus.Tasks().Start()
			slog.Info("workers started", slog.Int("num", configs.Config.Extractor.NumWorkers))
		}()
	}

	srv := &http.Server{
		Handler:           s,
		MaxHeaderBytes:    1 << 20,
		ReadHeaderTimeout: time.Second * 5,
	}
	var listenURL *url.URL
	serverURL := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%d", configs.Config.Server.Host, configs.Config.Server.Port),
		Path:   urls.Prefix(),
	}

	// Start the HTTP server
	go func() {
		var ln net.Listener
		host := configs.Config.Server.Host
		if strings.HasPrefix(host, "unix:") {
			socketURL, err := url.Parse(host)
			if err != nil {
				fatal("invalid UNIX socket URL", err)
			}

			unixAddr, err := net.ResolveUnixAddr("unix", socketURL.Path)
			if err != nil {
				fatal("failed to parse listen address "+host, err)
			}
			srv.Addr = unixAddr.Name
			ln, err = net.ListenUnix("unix", unixAddr)
			if err != nil {
				fatal("failed to listen on "+host, err)
			}

			mode := fs.FileMode(0o666)
			if m, err := strconv.ParseUint(socketURL.Query().Get("mode"), 0, 32); err == nil {
				mode = fs.FileMode(m)
			}
			if err = os.Chmod(socketURL.Path, mode); err != nil {
				fatal("failed to set mode on "+socketURL.Path, err)
			}

			listenURL = &url.URL{Scheme: "unix", Opaque: unixAddr.Name}
		} else {
			srv.Addr = net.JoinHostPort(
				configs.Config.Server.Host,
				strconv.Itoa(configs.Config.Server.Port),
			)
			var err error
			ln, err = net.Listen("tcp", srv.Addr)
			if err != nil {
				fatal("cannot start the server", err)
			}

			listenURL = &url.URL{Scheme: "tcp", Host: srv.Addr}
		}

		var err error
		if configs.Config.Server.CertFile != "" && configs.Config.Server.KeyFile != "" {
			// Set a certificate and keys when given
			srv.TLSConfig = &tls.Config{
				MinVersion: tls.VersionTLS12,
			}
			srv.TLSConfig.Certificates = make([]tls.Certificate, 1)
			srv.TLSConfig.Certificates[0], err = tls.LoadX509KeyPair(
				configs.Config.Server.CertFile,
				configs.Config.Server.KeyFile,
			)
			if err != nil {
				fatal("cannot configure TLS", err)
			}

			// If a CA file is present, it's for mTLS.
			// We don't set ClientAuth to [tls.RequireAndVerifyClientCert] and
			// it's up to any midleware or authentication provider to require
			// a client certificate.
			if configs.Config.Server.ClientCAFile != "" {
				caPem, err := os.ReadFile(configs.Config.Server.ClientCAFile)
				if err != nil {
					fatal("cannot load CA certificate", err)
				}
				p := x509.NewCertPool()
				if !p.AppendCertsFromPEM(caPem) {
					fatal("cannot load CA certificate", errors.New("could not parse"))
				}
				srv.TLSConfig.ClientCAs = p
				srv.TLSConfig.ClientAuth = tls.VerifyClientCertIfGiven
			}
		}

		ready <- true
		if srv.TLSConfig != nil {
			serverURL.Scheme = "https"
			err = srv.ServeTLS(ln, "", "")
		} else {
			// Wrap http/2 h2c
			h2 := &http2.Server{}
			srv.Handler = h2c.NewHandler(srv.Handler, h2)
			if err := http2.ConfigureServer(srv, h2); err != nil {
				fatal("cannot configure server", err)
			}

			err = srv.Serve(ln)
		}

		if err != nil {
			if err == http.ErrServerClosed {
				slog.Info("stopping server...")
				return
			}
			slog.Error("server error", slog.Any("err", err))
		}
	}()

	// Server is ready to accept requests
	<-ready
	if listenURL.Scheme == "unix" {
		slog.Info("server started", slog.String("addr", listenURL.String()))
	} else {
		switch serverURL.Hostname() {
		case "0.0.0.0", "127.0.0.1", "::", "::1":
			serverURL.Host = fmt.Sprintf("localhost:%d", configs.Config.Server.Port)
		}
		slog.Info("server started",
			slog.String("addr", listenURL.String()),
			slog.String("url", serverURL.String()),
		)
	}

	// Server shutdown
	<-stop
	slog.Info("shutting down...")

	// Graceful http shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", slog.Any("err", err))
	}
	slog.Info("server stopped")

	if startBus {
		slog.Info("stopping workers...")
		bus.Tasks().Stop()
		slog.Info("workers stopped")
	}

	return nil
}

// InitServer setups all the routes.
func InitServer(s *server.Server) error {
	components.InitCustomTemplates()
	server.DefaultErrorComponent = components.DefaultError

	// Init session store
	if err := server.InitSession(); err != nil {
		return err
	}

	// Static asserts
	assets.SetupRoutes(s)

	// Auth routes
	signin.SetupRoutes(s)
	oauth2.SetupRoutes(s)

	// Onboarding routes
	onboarding.SetupRoutes(s)

	// Dashboard routes
	dashboard.SetupRoutes(s)

	// Bookmark routes
	// - /bookmarks/*
	// - /bm/* (for bookmark media files)
	bookmark_routes.SetupRoutes(s)

	// OPDS routes
	opds.SetupRoutes(s)

	// User routes
	profile.SetupRoutes(s)

	// Admin routes
	admin.SetupRoutes(s)

	// Video player route
	videoplayer.SetupRoutes(s)

	// Help routes
	docs.SetupRoutes(s)

	// Cookbook routes
	cookbook.SetupRoutes(s)

	s.Init()
	return nil
}
