package store

import (
	"testing"
	"time"

	"github.com/shalarasgar/carpool-backend/internal/config"
)

func TestPoolConfig(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		DatabaseURL:         "postgres://carpool:carpool@localhost:5432/carpool?sslmode=disable",
		DBMaxConns:          7,
		DBMinConns:          2,
		DBMaxConnLifetime:   45 * time.Minute,
		DBMaxConnIdleTime:   3 * time.Minute,
		DBHealthCheckPeriod: 20 * time.Second,
	}

	poolCfg, err := PoolConfig(cfg)
	if err != nil {
		t.Fatalf("PoolConfig() error = %v", err)
	}
	if poolCfg.MaxConns != 7 || poolCfg.MinConns != 2 {
		t.Fatalf("conns = %d min %d", poolCfg.MaxConns, poolCfg.MinConns)
	}
	if poolCfg.MaxConnLifetime != 45*time.Minute {
		t.Fatalf("lifetime = %s", poolCfg.MaxConnLifetime)
	}
	if poolCfg.MaxConnIdleTime != 3*time.Minute {
		t.Fatalf("idle = %s", poolCfg.MaxConnIdleTime)
	}
	if poolCfg.HealthCheckPeriod != 20*time.Second {
		t.Fatalf("health check = %s", poolCfg.HealthCheckPeriod)
	}
	if poolCfg.ConnConfig.Database != "carpool" {
		t.Fatalf("database = %q", poolCfg.ConnConfig.Database)
	}
}

func TestPoolConfigRejectsInvalidURL(t *testing.T) {
	t.Parallel()

	_, err := PoolConfig(config.Config{DatabaseURL: "not a url"})
	if err == nil {
		t.Fatal("PoolConfig() error = nil")
	}
}
