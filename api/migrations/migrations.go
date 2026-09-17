// Package migrations embeds Nodera's SQL migration files so the compiled
// binary carries its own schema and doesn't depend on a filesystem path at
// runtime. The .sql files in this directory are the source of truth for the
// schema (rule 23: never depend on manually created production schema).
package migrations

import (
	"embed"
	"fmt"

	"github.com/nodera/nodera/internal/platform/db"
)

//go:embed *.sql
var files embed.FS

// Load reads every embedded .sql file and returns them as db.Migration
// values ready to pass to db.Migrate.
func Load() ([]db.Migration, error) {
	entries, err := files.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("migrations: failed to read embedded files: %w", err)
	}

	out := make([]db.Migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		version, name, err := db.ParseVersion(e.Name())
		if err != nil {
			return nil, err
		}
		content, err := files.ReadFile(e.Name())
		if err != nil {
			return nil, fmt.Errorf("migrations: failed to read %s: %w", e.Name(), err)
		}
		out = append(out, db.Migration{Version: version, Name: name, SQL: string(content)})
	}
	return out, nil
}
