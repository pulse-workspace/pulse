// Package app helpers provides utility functions for application bootstrap and configuration.
package app

import (
	"context"

	"github.com/pulse-workspace/pulse/internal/api/handlers"
	"github.com/pulse-workspace/pulse/pkg/shared/db"
)

// containsRole checks whether a target role exists within the active role set.
func containsRole(roles []string, want string) bool {
	for _, r := range roles {
		if r == want {
			return true
		}
	}
	return false
}

// maskDSN obfuscates database credentials in logs.
func maskDSN(dsn string) string {
	atIdx := -1
	for i := 0; i < len(dsn)-1; i++ {
		if dsn[i] == '@' {
			atIdx = i
			break
		}
	}
	if atIdx == -1 {
		return dsn
	}
	colonIdx := -1
	for i := 0; i < atIdx; i++ {
		if dsn[i] == ':' && i > 7 {
			colonIdx = i
			break
		}
	}
	if colonIdx == -1 {
		return dsn
	}
	return dsn[:colonIdx+1] + "***" + dsn[atIdx:]
}

// verifyAdminPassword queries the database and verifies hashed admin credentials.
func verifyAdminPassword(ctx context.Context, d *db.DB, userID, password string) (bool, error) {
	var hash string
	err := d.QueryRow(ctx,
		`SELECT password_hash FROM pulse_admins WHERE user_id = $1`, userID).Scan(&hash)
	if err != nil {
		return false, nil
	}
	return handlers.VerifyAdminPasswordHash(password, hash), nil
}

