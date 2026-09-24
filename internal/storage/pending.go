package storage

import (
	"context"
	"fmt"
)

// PendingMigrations lists the embedded migrations not yet recorded in
// schema_migrations, without applying anything — the read-only half of
// ApplyMigrations, for health checks. A database that has never been
// migrated reports every migration as pending.
func (d *Database) PendingMigrations(ctx context.Context) ([]string, error) {
	migrations, err := embeddedMigrations()
	if err != nil {
		return nil, err
	}
	var hasTable int
	if err := d.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&hasTable); err != nil {
		return nil, fmt.Errorf("inspecting schema_migrations: %w", err)
	}
	applied := map[int]bool{}
	if hasTable > 0 {
		rows, err := d.DB.QueryContext(ctx, `SELECT version FROM schema_migrations`)
		if err != nil {
			return nil, fmt.Errorf("reading schema_migrations: %w", err)
		}
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scanning schema_migrations: %w", err)
			}
			applied[v] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterating schema_migrations: %w", err)
		}
	}
	var pending []string
	for _, m := range migrations {
		if !applied[m.version] {
			pending = append(pending, m.filename)
		}
	}
	return pending, nil
}

// QuickCheck runs SQLite's PRAGMA quick_check and returns its first
// problem line, or "" when the database is intact.
func (d *Database) QuickCheck(ctx context.Context) (string, error) {
	var res string
	if err := d.DB.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&res); err != nil {
		return "", fmt.Errorf("quick_check: %w", err)
	}
	if res == "ok" {
		return "", nil
	}
	return res, nil
}
