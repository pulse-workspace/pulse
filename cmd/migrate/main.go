// Command migrate runs all pending SQL migrations against the configured database.
//
// Usage:
//
//	PULSE_POSTGRES_DSN="postgres://..." go run ./cmd/migrate up
//	go run ./cmd/migrate down      # rolls back 1 migration step
//	go run ./cmd/migrate version   # shows current schema version
//	go run ./cmd/migrate seed      # seeds demo accounts (Alice, Bob, Charlie) and #general channel
//
// Idempotent: tracks applied migrations in `pulse_schema_version`.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/pulse-im/pulse/pkg/shared/config"
	"github.com/pulse-im/pulse/pkg/shared/db"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: migrate <up|down|version|seed>")
		os.Exit(1)
	}
	cmd := os.Args[1]

	cfg, err := config.Load("configs/dev-localhost.yaml")
	if err != nil {
		cfg = config.Defaults()
	}
	if v := os.Getenv("PULSE_POSTGRES_DSN"); v != "" {
		cfg.Postgres.Host = ""
	}

	dsn := cfg.Postgres.DSN()
	if v := os.Getenv("PULSE_POSTGRES_DSN"); v != "" {
		dsn = v
	}

	ctx := context.Background()
	d, err := db.Open(ctx, dsn, 4)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer d.Close()

	migrationsDir, err := db.FindMigrationsDir(cfg.Postgres.MigrationsPath)
	if err != nil && cmd != "seed" {
		log.Fatalf("find migrations: %v", err)
	}

	switch cmd {
	case "up":
		n, err := db.ApplyUp(ctx, d, migrationsDir)
		if err != nil {
			log.Fatalf("up: %v", err)
		}
		fmt.Printf("successfully applied %d migration(s)\n", n)
	case "down":
		n, err := db.ApplyDown(ctx, d, migrationsDir, 1)
		if err != nil {
			log.Fatalf("down: %v", err)
		}
		fmt.Printf("successfully rolled back %d migration(s)\n", n)
	case "version":
		v, err := db.CurrentVersion(ctx, d)
		if err != nil {
			log.Fatalf("version: %v", err)
		}
		fmt.Printf("current schema version: %s\n", v)
	case "seed":
		if err := db.SeedDemoData(ctx, d); err != nil {
			log.Fatalf("seed demo data: %v", err)
		}
		fmt.Println("demo data seeded successfully (Alice, Bob, Charlie, Admin)")
	default:
		fmt.Printf("unknown command: %s\n", cmd)
		os.Exit(1)
	}
}
