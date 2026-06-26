package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"cairnfield/backend/blob"
	"cairnfield/backend/config"
	"cairnfield/backend/search"
	"cairnfield/backend/store"
	"cairnfield/backend/web"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := config.Load()
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	searchService, err := search.OpenPerUser(cfg.IndexPath)
	if err != nil {
		return err
	}
	defer searchService.Close()
	if err := rebuildSearchIndexes(context.Background(), db, searchService); err != nil {
		return err
	}

	handler := web.New(web.Options{
		Store:        db,
		Blobs:        blob.New(cfg.DataDir),
		Search:       searchService,
		SessionTTL:   cfg.SessionTTL,
		CookieSecure: cfg.CookieSecure,
		BasePath:     cfg.BasePath,
		StaticDir:    filepath.Join("frontend", "dist"),
		OIDC:         cfg.OIDC,
	}).Handler()

	srv := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Printf("cairnfield listening on %s", cfg.Addr)
	err = srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func rebuildSearchIndexes(ctx context.Context, db *store.Store, searchService *search.Service) error {
	users, err := db.ListUsers(ctx)
	if err != nil {
		return err
	}
	for _, user := range users {
		docs, err := db.SearchDocumentsForCurrentNotes(ctx, user.ID)
		if err != nil {
			return err
		}
		if err := searchService.Rebuild(ctx, user.ID, docs); err != nil {
			return err
		}
	}
	return nil
}
