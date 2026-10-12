// Command migrate applies Nodera's embedded SQL migrations and exits. It is the
// production schema-change step: run it (with the database OWNER role) after a
// release is pulled and before the API starts. Migrations are forward-only; see
// docs/DEPLOYMENT.md "Migrations" for the failure and rollback expectations.
//
//	NODERA_MIGRATE_DATABASE_URL[_FILE]  owner/migration role URL (falls back to NODERA_DATABASE_URL)
//	nodera-migrate            apply pending migrations
//	nodera-migrate -status    list pending migrations, change nothing (exit 3 if any)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nodera/nodera/internal/platform/config"
	"github.com/nodera/nodera/internal/platform/db"
	"github.com/nodera/nodera/migrations"
)

func main() {
	status := flag.Bool("status", false, "report pending migrations without applying them")
	flag.Parse()

	url, err := config.SecretFromEnv("NODERA_MIGRATE_DATABASE_URL") // or ..._FILE
	if err == nil && url == "" {
		url, err = config.SecretFromEnv("NODERA_DATABASE_URL")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(2)
	}
	if url == "" {
		fmt.Fprintln(os.Stderr, "migrate: NODERA_MIGRATE_DATABASE_URL (or NODERA_DATABASE_URL) is required")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	pool, err := db.Connect(ctx, url, 2)
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	defer pool.Close()
	migs, err := migrations.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	pending, err := db.Pending(ctx, pool, migs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	if *status {
		fmt.Printf("pending migrations: %v\n", pending)
		if len(pending) > 0 {
			os.Exit(3)
		}
		return
	}
	if err := db.Migrate(ctx, pool, migs); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	fmt.Printf("migrate: ok (%d pending applied, %d total)\n", len(pending), len(migs))
}
