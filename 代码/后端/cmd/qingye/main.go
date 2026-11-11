// qingye serves the isolated integration API and runs explicit maintenance jobs.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"qingye/backend/internal/bootstrap"
	"qingye/backend/internal/config"
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
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	a, err := bootstrap.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer a.Close()
	switch command {
	case "migrate":
		return a.Maintenance.Migrate(ctx)
	case "refresh-itemcf":
		return a.Recommendations.RefreshItemCF(ctx, time.Time{})
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
		id, e := a.Maintenance.SeedUser(ctx, input)
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
		count, e := a.Maintenance.ImportLegacy(ctx, f)
		if e == nil {
			fmt.Println("imported products", count)
		}
		return e
	case "reconcile-media":
		result, e := a.Maintenance.ReconcileMedia(ctx, *limit)
		if e == nil {
			fmt.Printf("checked=%d ready=%d pending=%d invalid=%d\n", result.Checked, result.Ready, result.Pending, result.Invalid)
		}
		return e
	case "serve":
		return a.Serve(ctx)
	default:
		return fmt.Errorf("unknown command %q; use serve, migrate, seed-user, import-legacy, reconcile-media, refresh-itemcf", command)
	}
}
