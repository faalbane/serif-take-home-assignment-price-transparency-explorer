// Command server ingests CMS Transparency in Coverage rate files and serves the
// search API and, optionally, the built frontend.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/faalbane/price-transparency-explorer/internal/api"
	"github.com/faalbane/price-transparency-explorer/internal/config"
	"github.com/faalbane/price-transparency-explorer/internal/fetch"
	"github.com/faalbane/price-transparency-explorer/internal/ingest"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmsgprefix)

	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		logger.Fatalf("configuration error: %v", err)
	}

	f := fetch.New(cfg.CacheDir)
	f.Logf = logger.Printf
	f.AllowLocalFiles = cfg.AllowLocalFiles

	svc := ingest.NewService(ingest.Config{
		IndexURL:        cfg.IndexURL,
		Fetcher:         f,
		Logf:            logger.Printf,
		MaxItemsPerFile: cfg.MaxItemsPerFile,
		MaxFiles:        cfg.MaxFiles,
		Offline:         cfg.Offline,
	})

	srv := api.NewServer(svc, cfg.IndexURL)
	handler := srv.Routes(api.Options{
		StaticDir:      cfg.StaticDir,
		AllowedOrigins: cfg.AllowedOrigins,
		Logger:         logger,
	})

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Generous write timeout: a CSV export of a large result set is a
		// legitimate slow response.
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	// Ingest runs in the background so the server is reachable immediately and
	// the UI can show real progress instead of failing to connect.
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Printf("ingest starting from index %s", cfg.IndexURL)
		if err := svc.Run(rootCtx); err != nil {
			// Already recorded in the ingest status and surfaced through the
			// API; the server stays up so the failure is visible in the UI.
			logger.Printf("ingest failed: %v", err)
		}
	}()

	go func() {
		logger.Printf("listening on %s (static dir: %q)", cfg.Addr, cfg.StaticDir)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("server error: %v", err)
		}
	}()

	<-rootCtx.Done()
	logger.Printf("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Printf("graceful shutdown failed: %v", err)
	}
}
