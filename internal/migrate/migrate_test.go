package migrate

import (
	"bytes"
	"errors"
	"io"
	"net/url"
	"os"
	"reflect"
	"testing"

	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/shalarasgar/carpool-backend/migrations"
)

func TestPGX5URL(t *testing.T) {
	t.Parallel()

	got, err := pgx5URL("postgres://carpool:carpool@localhost:5432/carpool?sslmode=disable")
	if err != nil {
		t.Fatalf("pgx5URL() error = %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if parsed.Scheme != "pgx5" || parsed.Host != "localhost:5432" || parsed.Path != "/carpool" {
		t.Fatalf("url = %s", got)
	}
	if parsed.Query().Get("sslmode") != "disable" {
		t.Fatalf("sslmode = %q", parsed.Query().Get("sslmode"))
	}
	if parsed.Query().Get("default_query_exec_mode") != "simple_protocol" {
		t.Fatalf("exec mode = %q", parsed.Query().Get("default_query_exec_mode"))
	}

	got, err = pgx5URL("postgresql://carpool:carpool@postgres:5432/carpool")
	if err != nil {
		t.Fatalf("pgx5URL(postgresql) error = %v", err)
	}
	parsed, err = url.Parse(got)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if parsed.Scheme != "pgx5" || parsed.Hostname() != "postgres" {
		t.Fatalf("url = %s", got)
	}
	if parsed.Query().Get("default_query_exec_mode") != "simple_protocol" {
		t.Fatalf("exec mode = %q", parsed.Query().Get("default_query_exec_mode"))
	}

	if _, err := pgx5URL("mysql://localhost/db"); err == nil {
		t.Fatal("pgx5URL(mysql) error = nil")
	}
}

func TestEmbeddedMigrations(t *testing.T) {
	t.Parallel()

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("iofs.New: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	var versions []uint
	version, err := src.First()
	if err != nil {
		t.Fatalf("First: %v", err)
	}
	for {
		versions = append(versions, version)
		up, _, err := src.ReadUp(version)
		if err != nil {
			t.Fatalf("ReadUp %d: %v", version, err)
		}
		body, err := io.ReadAll(up)
		_ = up.Close()
		if err != nil {
			t.Fatalf("read up %d: %v", version, err)
		}
		if len(bytes.TrimSpace(body)) == 0 {
			t.Fatalf("migration %d up file is empty", version)
		}
		down, _, err := src.ReadDown(version)
		if err != nil {
			t.Fatalf("ReadDown %d: %v", version, err)
		}
		_ = down.Close()

		next, err := src.Next(version)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			t.Fatalf("Next %d: %v", version, err)
		}
		version = next
	}

	want := []uint{1, 2, 3}
	if !reflect.DeepEqual(versions, want) {
		t.Fatalf("versions = %v, want %v", versions, want)
	}
}
