package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaults(t *testing.T) {
	c := Defaults()
	require.NotNil(t, c)
	require.Equal(t, "cell-0", c.Cell.ID)
	require.Equal(t, "us-east-1", c.ObjectS3.Region)
	require.Equal(t, ":32001", c.GW.Listen)
	require.Equal(t, ":32011", c.API.Listen)
	require.Equal(t, "pulse.protocol.v1.msgpack", c.GW.Subprotocol)
	require.True(t, c.Roles.Cell)
	require.True(t, c.Roles.Gateway)
}

func TestLoadFromBytes(t *testing.T) {
	yaml := `
version: 0.1.0
cell:
  id: cell-7
  region: eu-west
roles:
  cell: true
  gateway: false
  storage: false
  license: false
  admin: false
postgres:
  host: db.internal
  port: 5432
`
	t.Setenv("PULSE_DEV_MODE", "1")
	c, err := LoadFromBytes([]byte(yaml))
	require.NoError(t, err)
	require.Equal(t, "0.1.0", c.Version)
	require.Equal(t, "cell-7", c.Cell.ID)
	require.Equal(t, "eu-west", c.Cell.Region)
	require.True(t, c.Roles.Cell)
	require.False(t, c.Roles.Gateway)
	// Missing fields should fall back to defaults.
	require.Equal(t, ":32011", c.API.Listen)
}

func TestRolesList(t *testing.T) {
	c := Defaults()
	roles := c.RolesList()
	require.Contains(t, roles, "cell")
	require.Contains(t, roles, "gateway")
	require.Contains(t, roles, "storage")
	require.Contains(t, roles, "license")
	require.Contains(t, roles, "admin")
}

func TestAdminInitSecret(t *testing.T) {
	c := Defaults()
	// Default falls back to "dev-mode" when PULSE_ADMIN_SECRET unset.
	require.Equal(t, "dev-mode", c.AdminInitSecret())
}

// =====================================================================
// Additional configuration tests below.
// Existing tests above are kept verbatim; the new ones target branches
// the original suite skipped (DSN SSL default, env override, production
// guards, String redaction, RolesList all-off, Load error paths).
// =====================================================================

// TestPostgresConfig_DSN_SSLDefault confirms an empty SSLMode falls
// back to "disable" — the dev-friendly default. Production deployments
// MUST set ssl_mode explicitly.
func TestPostgresConfig_DSN_SSLDefault(t *testing.T) {
	p := PostgresConfig{
		Host:     "db.local",
		Port:     5432,
		Database: "pulse",
		User:     "pulse",
		Password: "pw",
	}
	dsn := p.DSN()
	require.Contains(t, dsn, "sslmode=disable",
		"empty SSLMode must default to 'disable', got: %s", dsn)
	require.Contains(t, dsn, "pulse:pw@db.local:5432/pulse")
}

// TestPostgresConfig_DSN_SSLExplicit confirms a non-empty SSLMode is
// passed through verbatim.
func TestPostgresConfig_DSN_SSLExplicit(t *testing.T) {
	p := PostgresConfig{
		Host:     "db.prod",
		Port:     5432,
		Database: "pulse",
		User:     "pulse",
		Password: "pw",
		SSLMode:  "verify-full",
	}
	require.Contains(t, p.DSN(), "sslmode=verify-full")
}

// TestLoad_FileNotFound confirms a missing path surfaces a wrapped
// os.ReadFile error rather than a panic.
func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/pulse-config.yaml")
	require.Error(t, err)
}

// TestLoad_InvalidYAML confirms a malformed YAML body returns a parse
// error wrapped with the file path.
func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("not: valid: yaml: at: all:"), 0o600))
	_, err := Load(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse config",
		"error must wrap the yaml parse failure, got: %v", err)
}

// TestLoadFromBytes_EnvJWTSecretOverridesDefault confirms the
// PULSE_JWT_SECRET env var takes precedence over YAML + defaults.
func TestLoadFromBytes_EnvJWTSecretOverridesDefault(t *testing.T) {
	t.Setenv("PULSE_DEV_MODE", "1")
	t.Setenv("PULSE_JWT_SECRET", "real-secret-from-env-32-characters-long-yes")
	cfg, err := LoadFromBytes([]byte(""))
	require.NoError(t, err)
	require.Equal(t, "real-secret-from-env-32-characters-long-yes", cfg.Auth.JWTSecret)
}

// TestLoadFromBytes_ProductionRefusesDevSecret confirms the production
// guard: when PULSE_DEV_MODE != "1" and the loaded JWT secret matches
// the dev default, LoadFromBytes returns an explicit error.
func TestLoadFromBytes_ProductionRefusesDevSecret(t *testing.T) {
	t.Setenv("PULSE_DEV_MODE", "")
	t.Setenv("PULSE_JWT_SECRET", "")
	_, err := LoadFromBytes([]byte(""))
	require.Error(t, err)
	require.Contains(t, err.Error(), "PULSE_JWT_SECRET",
		"error must explain the JWT secret requirement, got: %v", err)
}

// TestLoadFromBytes_ProductionRefusesShortSecret confirms the
// minimum-length guard. A too-short JWT secret is rejected.
func TestLoadFromBytes_ProductionRefusesShortSecret(t *testing.T) {
	t.Setenv("PULSE_DEV_MODE", "")
	t.Setenv("PULSE_JWT_SECRET", "short")
	_, err := LoadFromBytes([]byte(""))
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 32 characters")
}

// TestRolesList_AllRolesToggled confirms every role appears when its
// toggle is true, and the list is empty when all toggles are false.
func TestRolesList_AllRolesToggled(t *testing.T) {
	cases := []struct {
		name  string
		roles Roles
		want  []string
	}{
		{"all false", Roles{}, nil},
		{"cell only", Roles{Cell: true}, []string{"cell"}},
		{"cell + gateway", Roles{Cell: true, Gateway: true}, []string{"cell", "gateway"}},
		{
			"all on",
			Roles{Cell: true, Gateway: true, Storage: true, License: true, Admin: true, Backup: true},
			[]string{"cell", "gateway", "storage", "license", "admin", "backup"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Roles: tc.roles}
			got := c.RolesList()
			if tc.want == nil {
				require.Empty(t, got, "all-off should produce no roles")
				return
			}
			require.Equal(t, tc.want, got)
		})
	}
}

// TestAdminInitSecret_EnvOverrides confirms the env var path: when
// PULSE_ADMIN_SECRET is set, it wins over the dev-mode default.
func TestAdminInitSecret_EnvOverrides(t *testing.T) {
	t.Setenv("PULSE_ADMIN_SECRET", "real-bootstrap-secret")
	c := &Config{}
	require.Equal(t, "real-bootstrap-secret", c.AdminInitSecret())
}

// TestString_ContainsExpectedFields confirms the String() output
// includes the cell id, region, roles, postgres host:port/database,
// nats url, object storage backend+bucket, and listen addresses.
func TestString_ContainsExpectedFields(t *testing.T) {
	c := Defaults()
	c.Cell.ID = "cell-test"
	c.Cell.Region = "us-west-2"
	c.Roles = Roles{Cell: true, Gateway: true}
	s := c.String()
	require.Contains(t, s, "cell=cell-test")
	require.Contains(t, s, "region=us-west-2")
	require.Contains(t, s, "postgres=")
	require.Contains(t, s, "nats=")
	require.Contains(t, s, "object=")
	require.Contains(t, s, "gateway_listen=")
	require.Contains(t, s, "api_listen=")
}

// TestString_RedactsSecrets confirms the password never appears in
// the String output. This is a regression guard for the redaction
// contract operators rely on when sharing logs.
func TestString_RedactsSecrets(t *testing.T) {
	c := Defaults()
	c.Postgres.Password = "super-secret-pw"
	c.Auth.JWTSecret = "super-secret-jwt"
	s := c.String()
	require.NotContains(t, s, "super-secret-pw",
		"postgres password must be redacted from String()")
	require.NotContains(t, s, "super-secret-jwt",
		"JWT secret must be redacted from String()")
	require.NotEmpty(t, strings.TrimSpace(s))
}
