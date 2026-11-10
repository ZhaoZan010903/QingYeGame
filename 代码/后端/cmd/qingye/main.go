// qingye serves the isolated integration API and runs explicit maintenance jobs.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"qingye/backend/internal/app"
)

func main() {
	if err := run(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}
func run() error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	products := flags.String("products", "", "path to private legacy product JSON")
	file := flags.String("file", "", "private seed-user JSON file; omitted reads stdin")
	limit := flags.Int("limit", 0, "maximum pending media records to reconcile, 0 means all")
	if len(os.Args) > 2 {
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := app.New(ctx, app.EnvConfig())
	if err != nil {
		return err
	}
	defer a.Close()
	switch command {
	case "migrate":
		return a.Migrate(ctx)
	case "seed-user":
		var input io.Reader = os.Stdin
		if *file != "" {
			f, e := os.Open(*file)
			if e != nil {
				return e
			}
			defer f.Close()
			input = f
		}
		id, e := a.SeedUser(ctx, input)
		if e == nil {
			fmt.Println("seeded user", id)
		}
		return e
	case "import-legacy":
		if *products == "" {
			return fmt.Errorf("--products is required")
		}
		f, e := os.Open(*products)
		if e != nil {
			return e
		}
		defer f.Close()
		count, e := a.ImportLegacy(ctx, f)
		if e == nil {
			fmt.Println("imported products", count)
		}
		return e
	case "reconcile-media":
		result, e := a.ReconcileMedia(ctx, *limit)
		if e == nil {
			fmt.Printf("checked=%d ready=%d pending=%d invalid=%d\n", result.Checked, result.Ready, result.Pending, result.Invalid)
		}
		return e
	case "serve":
		return serve(ctx, a)
	default:
		return fmt.Errorf("unknown command %q; use serve, migrate, seed-user, import-legacy, reconcile-media", command)
	}
}
func serve(ctx context.Context, a *app.App) error {
	server := &http.Server{Addr: a.Config.Addr, Handler: a.Routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	errors := make(chan error, 1)
	go func() {
		slog.Info("integration API listening", "address", a.Config.Addr)
		errors <- server.ListenAndServe()
	}()
	// Recommendations are derived, recoverable data; API availability does not depend on a batch.
	go func() {
		// The public API owns the periodic job; the admin API can trigger it on demand.
		if a.Config.Surface == "admin" {
			return
		}
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			job, cancel := context.WithTimeout(ctx, 45*time.Second)
			if e := a.RefreshRecommendations(job); e != nil && ctx.Err() == nil {
				slog.Warn("recommendation refresh", "error", e)
			}
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	select {
	case e := <-errors:
		if e != http.ErrServerClosed {
			return e
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
