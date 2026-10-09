// Package migrate applies the embedded SQL migrations with golang-migrate.
package migrate

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the pgx5 database driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/shalarasgar/carpool-backend/migrations"
)

// Up applies every pending migration. ErrNoChange is treated as success.
func Up(databaseURL string) error {
	m, err := open(databaseURL)
	if err != nil {
		return err
	}
	defer closeMigrate(m)

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

func open(databaseURL string) (*migrate.Migrate, error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("migration source: %w", err)
	}
	dsn, err := pgx5URL(databaseURL)
	if err != nil {
		return nil, err
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return nil, fmt.Errorf("open migrations: %w", err)
	}
	return m, nil
}

func closeMigrate(m *migrate.Migrate) {
	sourceErr, dbErr := m.Close()
	_ = sourceErr
	_ = dbErr
}

// pgx5URL rewrites a postgres URL for the golang-migrate pgx5 driver.
// simple_protocol lets one migration file contain many statements, including
// the dollar-quoted updated_at function. The extended protocol rejects that.
func pgx5URL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "postgres://"):
		raw = "pgx5://" + strings.TrimPrefix(raw, "postgres://")
	case strings.HasPrefix(raw, "postgresql://"):
		raw = "pgx5://" + strings.TrimPrefix(raw, "postgresql://")
	case strings.HasPrefix(raw, "pgx5://"):
	default:
		return "", fmt.Errorf("database url must start with postgres:// or postgresql://")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("database url: %w", err)
	}
	query := parsed.Query()
	if query.Get("default_query_exec_mode") == "" {
		query.Set("default_query_exec_mode", "simple_protocol")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
