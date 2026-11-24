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

	"qingye/backend/internal/application/legacysync"
	"qingye/backend/internal/bootstrap"
	"qingye/backend/internal/config"
	"qingye/backend/internal/infrastructure/legacyfeed"
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
	checkSource := flags.Bool("check-source", false, "read the full legacy catalog without connecting to the new site")
	if len(os.Args) > 2 {
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if command == "legacy-sync" && *checkSource {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		source, err := legacyfeed.NewSaaS().Catalog(ctx, nil)
		if err != nil {
			return err
		}
		fmt.Printf("legacy source checked: %d products\n", len(source.Records))
		if source.Latest != nil {
			fmt.Printf("latest_source_update=%s\n", source.Latest.UTC().Format(time.RFC3339))
		}
		return nil
	}
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
	case "legacy-sync":
		if a.LegacySync == nil {
			return fmt.Errorf("legacy catalog sync requires managed OSS image storage")
		}
		batch, err := a.LegacySync.RunScheduled(ctx, legacysync.ScheduledNoon(time.Now()))
		if batch.ID != "" {
			fmt.Printf("legacy sync batch %s: %s\n", batch.ID, batch.Status)
		} else if err == nil {
			fmt.Println("legacy sync skipped: schedule paused, already run, or another batch active")
		}
		return err
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
		return fmt.Errorf("unknown command %q; use serve, migrate, seed-user, import-legacy, reconcile-media, refresh-itemcf, legacy-sync", command)
	}
}
