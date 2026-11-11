// Package db provides database access, migration runners, and demo data seeding.
package db

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/argon2"
)

// FindMigrationsDir looks for the migrations directory starting from hint,
// falling back to common relative and absolute locations.
func FindMigrationsDir(hint string) (string, error) {
	candidates := []string{}
	if hint != "" {
		candidates = append(candidates, hint)
	}
	candidates = append(candidates,
		"migrations",
		"../migrations",
		"../../migrations",
		"/migrations",
	)

	for _, c := range candidates {
		info, err := os.Stat(c)
		if err == nil && info.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("migrations directory not found (checked %v)", candidates)
}

// CurrentVersion returns the most recently applied migration version.
func CurrentVersion(ctx context.Context, d *DB) (string, error) {
	var v string
	err := d.Pool().QueryRow(ctx,
		`SELECT version FROM pulse_schema_version ORDER BY version DESC LIMIT 1`,
	).Scan(&v)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "(none)", nil
		}
		// Table might not exist yet
		return "(none)", nil
	}
	return v, nil
}

// LoadApplied returns a set of all migration versions already applied.
func LoadApplied(ctx context.Context, d *DB) (map[string]bool, error) {
	out := map[string]bool{}
	rows, err := d.Pool().Query(ctx, `SELECT version FROM pulse_schema_version`)
	if err != nil {
		// Table might not exist yet — first migration creates it.
		return out, nil
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

// ApplyUp applies all pending .up.sql migrations from dir in alphanumeric order.
func ApplyUp(ctx context.Context, d *DB, dir string) (int, error) {
	pattern := filepath.Join(dir, "*.up.sql")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return 0, err
	}
	sort.Strings(files)

	applied, err := LoadApplied(ctx, d)
	if err != nil {
		return 0, err
	}

	appliedCount := 0
	for _, f := range files {
		base := filepath.Base(f)
		version := strings.TrimSuffix(base, ".up.sql")
		if applied[version] {
			continue
		}

		body, err := os.ReadFile(f)
		if err != nil {
			return appliedCount, fmt.Errorf("read %s: %w", f, err)
		}

		log.Printf("pulse migrate: applying %s ...", base)
		if _, err := d.Pool().Exec(ctx, string(body)); err != nil {
			return appliedCount, fmt.Errorf("apply %s: %w", f, err)
		}

		// Ensure pulse_schema_version exists before recording
		_, _ = d.Pool().Exec(ctx, `
			CREATE TABLE IF NOT EXISTS pulse_schema_version (
				version    VARCHAR(64) PRIMARY KEY,
				applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				notes      TEXT
			)`)

		if _, err := d.Pool().Exec(ctx,
			`INSERT INTO pulse_schema_version (version, applied_at, notes)
			 VALUES ($1, NOW(), $2)
			 ON CONFLICT (version) DO NOTHING`,
			version, "applied by pulse migration runner"); err != nil {
			return appliedCount, fmt.Errorf("record version %s: %w", version, err)
		}
		appliedCount++
		log.Printf("pulse migrate: OK %s", base)
	}

	return appliedCount, nil
}

// ApplyDown rolls back the last n applied migrations using their .down.sql files.
func ApplyDown(ctx context.Context, d *DB, dir string, steps int) (int, error) {
	if steps <= 0 {
		steps = 1
	}

	// Fetch applied migrations in descending order
	rows, err := d.Pool().Query(ctx, `SELECT version FROM pulse_schema_version ORDER BY version DESC`)
	if err != nil {
		return 0, fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()

	var appliedVersions []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return 0, err
		}
		appliedVersions = append(appliedVersions, v)
	}

	if len(appliedVersions) == 0 {
		log.Println("pulse migrate: no migrations to rollback")
		return 0, nil
	}

	revertedCount := 0
	for i := 0; i < steps && i < len(appliedVersions); i++ {
		version := appliedVersions[i]
		downFile := filepath.Join(dir, version+".down.sql")
		body, err := os.ReadFile(downFile)
		if err != nil {
			return revertedCount, fmt.Errorf("read %s: %w", downFile, err)
		}

		log.Printf("pulse migrate: reverting %s ...", version)
		if _, err := d.Pool().Exec(ctx, string(body)); err != nil {
			return revertedCount, fmt.Errorf("revert %s: %w", version, err)
		}

		if _, err := d.Pool().Exec(ctx, `DELETE FROM pulse_schema_version WHERE version = $1`, version); err != nil {
			return revertedCount, fmt.Errorf("remove version record %s: %w", version, err)
		}
		revertedCount++
		log.Printf("pulse migrate: reverted OK %s", version)
	}

	return revertedCount, nil
}

// AutoMigrate discovers migrations and applies all pending up migrations.
func AutoMigrate(ctx context.Context, d *DB, hint string) error {
	dir, err := FindMigrationsDir(hint)
	if err != nil {
		return err
	}
	count, err := ApplyUp(ctx, d, dir)
	if err != nil {
		return err
	}
	if count > 0 {
		log.Printf("pulse auto-migrate: applied %d migration(s) successfully from %s", count, dir)
	}
	return nil
}

// SeedDemoData creates initial admin credentials, demo users (Alice, Bob, Charlie),
// friendships, and a public '#general' group conversation.
func SeedDemoData(ctx context.Context, d *DB) error {
	// 1. Ensure pulse_admins table and seed admin
	_, _ = d.Pool().Exec(ctx, `
		CREATE TABLE IF NOT EXISTS pulse_admins (
			user_id        VARCHAR(64) PRIMARY KEY,
			password_hash  VARCHAR(255) NOT NULL,
			is_bootstrap   BOOLEAN NOT NULL DEFAULT false,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)

	adminHash, err := HashPassword("Pulse@Admin1")
	if err == nil {
		_, _ = d.Pool().Exec(ctx, `
			INSERT INTO pulse_admins (user_id, password_hash, is_bootstrap, created_at)
			VALUES ('admin', $1, true, NOW())
			ON CONFLICT (user_id) DO NOTHING
		`, adminHash)
	}

	// 2. Demo Users
	demoUsers := []struct {
		ID       string
		Nickname string
		Email    string
		Avatar   string
	}{
		{
			ID:       "u_alice",
			Nickname: "Alice Wonderland",
			Email:    "alice@pulse.local",
			Avatar:   "https://api.dicebear.com/7.x/avataaars/svg?seed=Alice",
		},
		{
			ID:       "u_bob",
			Nickname: "Bob Builder",
			Email:    "bob@pulse.local",
			Avatar:   "https://api.dicebear.com/7.x/avataaars/svg?seed=Bob",
		},
		{
			ID:       "u_charlie",
			Nickname: "Charlie Brown",
			Email:    "charlie@pulse.local",
			Avatar:   "https://api.dicebear.com/7.x/avataaars/svg?seed=Charlie",
		},
	}

	for _, u := range demoUsers {
		_, _ = d.Pool().Exec(ctx, `
			INSERT INTO users (user_id, nickname, avatar, email, phone, status, created_at, updated_at, token_version)
			VALUES ($1, $2, $3, $4, '', 'active', NOW(), NOW(), 1)
			ON CONFLICT (user_id) DO UPDATE SET nickname = EXCLUDED.nickname, avatar = EXCLUDED.avatar
		`, u.ID, u.Nickname, u.Avatar, u.Email)
	}

	// 3. Friendships (Alice <-> Bob, Alice <-> Charlie, Bob <-> Charlie)
	pairs := [][2]string{
		{"u_alice", "u_bob"},
		{"u_alice", "u_charlie"},
		{"u_bob", "u_charlie"},
	}
	for _, p := range pairs {
		_, _ = d.Pool().Exec(ctx, `
			INSERT INTO friendships (user_id, friend_id, status, created_at)
			VALUES ($1, $2, 'accepted', NOW()), ($2, $1, 'accepted', NOW())
			ON CONFLICT (user_id, friend_id) DO NOTHING
		`, p[0], p[1])
	}

	// 4. Sample Public Group Channel "#general"
	groupID := "g_general"
	convID := "c_g_general"

	_, _ = d.Pool().Exec(ctx, `
		INSERT INTO groups (group_id, name, avatar, owner_id, group_type, member_count, max_members, introduction, created_at, updated_at)
		VALUES ($1, 'General Chat', 'https://api.dicebear.com/7.x/shapes/svg?seed=general', 'u_alice', 'normal', 3, 2000, 'Welcome to Pulse General Discussion!', NOW(), NOW())
		ON CONFLICT (group_id) DO NOTHING
	`, groupID)

	for _, u := range demoUsers {
		_, _ = d.Pool().Exec(ctx, `
			INSERT INTO group_members (group_id, user_id, role, joined_at, nickname)
			VALUES ($1, $2, 'member', NOW(), $3)
			ON CONFLICT (group_id, user_id) DO NOTHING
		`, groupID, u.ID, u.Nickname)
	}

	// 5. Conversation for the group
	_, _ = d.Pool().Exec(ctx, `
		INSERT INTO conversations (conv_id, conv_type, participants, created_at, updated_at, pinned)
		VALUES ($1, 2, ARRAY['u_alice', 'u_bob', 'u_charlie'], NOW(), NOW(), true)
		ON CONFLICT (conv_id) DO NOTHING
	`, convID)

	// Direct conversation between Alice and Bob
	directConvID := "c_alice_bob"
	_, _ = d.Pool().Exec(ctx, `
		INSERT INTO conversations (conv_id, conv_type, participants, created_at, updated_at, pinned)
		VALUES ($1, 1, ARRAY['u_alice', 'u_bob'], NOW(), NOW(), false)
		ON CONFLICT (conv_id) DO NOTHING
	`, directConvID)

	log.Println("pulse seeder: demo users (Alice, Bob, Charlie) and #general channel seeded successfully")
	return nil
}

// HashPassword hashes password using Argon2id.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=1,p=4$%s$%s", b64Salt, b64Hash), nil
}

// VerifyPassword verifies an Argon2id hash.
func VerifyPassword(password, encodedHash string) bool {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	actualHash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, uint32(len(expectedHash)))
	return subtle.ConstantTimeCompare(actualHash, expectedHash) == 1
}
