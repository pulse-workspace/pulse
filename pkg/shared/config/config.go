// Package config provides typed configuration loaded from YAML.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level Pulse configuration.
type Config struct {
	Version string `yaml:"version"`
	Cell    Cell   `yaml:"cell"`
	Roles   Roles  `yaml:"roles"`

	Postgres PostgresConfig `yaml:"postgres"`
	NATS     NATSConfig     `yaml:"nats"`
	ObjectS3 S3Config       `yaml:"object_storage"`

	Auth AuthConfig `yaml:"auth"`
	Log  LogConfig  `yaml:"log"`
	API  APIConfig  `yaml:"api"`
	GW   GWConfig   `yaml:"gateway"`

	License LicenseConfig `yaml:"license"`
	Backup  BackupConfig  `yaml:"backup"`
	Media   MediaConfig   `yaml:"media"`
}

// MediaConfig configures media attachment storage.
//
// MaxUploadBytes caps the multipart body the cell accepts. Defaults to
// 50 MiB (see internal/cell/media.DefaultMaxUploadBytes) so a deployer
// who forgets the override still has a sane bound.
//
// AllowedMimes is the per-deployment override for the default allow-list
// declared in internal/cell/media/types.go (defaultAllowedMimes). An
// empty slice falls back to the default; a non-empty slice REPLACES it
// entirely (no merge semantics — operators get what they asked for).
type MediaConfig struct {
	MaxUploadBytes int64    `yaml:"max_upload_bytes"` // 0 = use package default
	AllowedMimes   []string `yaml:"allowed_mimes"`    // empty = use package default
}

// BackupConfig configures backup schedules and targets.
// When Enable is true, the process runs scheduled Postgres PITR + NATS
// JetStream snapshot operations and uploads artifacts to S3-compatible
// storage. All required env-derived fields fail fast per dev-rules §Env
// granularity (no fallback defaults).
type BackupConfig struct {
	Enable        bool     `yaml:"enable"`
	Schedule      string   `yaml:"schedule"` // cron expression
	RetentionDays int      `yaml:"retention_days"`
	WALGBin       string   `yaml:"wal_g_bin"`        // path to wal-g binary
	PostgresDSN   string   `yaml:"postgres_dsn"`     // libpq DSN; empty = use cfg.Postgres
	WALGEnvPrefix string   `yaml:"wal_g_env_prefix"` // env prefix for wal-g config (e.g. "WALG_")
	S3            S3Config `yaml:"s3"`               // backup destination bucket
	Concurrency   int      `yaml:"concurrency"`      // parallel upload jobs
}

// Cell identifies which cell this process belongs to.
type Cell struct {
	ID            string   `yaml:"id"`
	Region        string   `yaml:"region"`
	UserShardFrom int      `yaml:"user_shard_from"` // inclusive
	UserShardTo   int      `yaml:"user_shard_to"`   // exclusive
	Modules       []string `yaml:"modules"`
	// Peers lists the cell IDs of every other cell in the cluster.
	// Empty for single-cell deployments. Used by the cross-cell
	// router (cmd/pulse/main.go wires each into
	// routerSvc.RegisterPeer) so the outbound fan-out has a
	// destination list.
	//
	// Override via env var PULSE_CELL_PEERS as a comma-separated list
	// (e.g. "PULSE_CELL_PEERS=cell-A,cell-B,cell-C"). The env var
	// is for containerised deployments where the YAML isn't
	// templated; production uses the YAML directly.
	Peers []string `yaml:"peers"`
}

// Roles toggles which subsystems to run in this process.
type Roles struct {
	Cell    bool `yaml:"cell"`
	Gateway bool `yaml:"gateway"`
	Storage bool `yaml:"storage"`
	License bool `yaml:"license"`
	Admin   bool `yaml:"admin"`
	Backup  bool `yaml:"backup"`
}

// PostgresConfig configures the per-cell Postgres.
type PostgresConfig struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	Database       string `yaml:"database"`
	User           string `yaml:"user"`
	Password       string `yaml:"password"`
	SSLMode        string `yaml:"ssl_mode"`
	MaxConns       int    `yaml:"max_conns"`
	MinConns       int    `yaml:"min_conns"`
	MigrationsPath string `yaml:"migrations_path"`
}

// DSN returns a libpq-style DSN string.
func (p PostgresConfig) DSN() string {
	ssl := p.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		p.User, p.Password, p.Host, p.Port, p.Database, ssl)
}

// NATSConfig configures NATS JetStream connection.
type NATSConfig struct {
	URL          string `yaml:"url"`
	ClusterID    string `yaml:"cluster_id"`
	CellID       string `yaml:"cell_id"`
	StreamPrefix string `yaml:"stream_prefix"`
	TLS          bool   `yaml:"tls"`
	TLSCert      string `yaml:"tls_cert"`
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
}

// S3Config configures the S3-compatible object storage.
type S3Config struct {
	Backend   string `yaml:"backend"` // s3 | rustfs | minio | gcs | azure
	Endpoint  string `yaml:"endpoint"`
	Region    string `yaml:"region"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	PublicURL string `yaml:"public_url"`
	// Secure controls whether the minio-go client uses HTTPS for the
	// backend connection. Defaults to false in the dev Defaults() block
	// (MinIO local + reverse proxy is the typical OSS setup) but every
	// production-grade backend must set this to true. Real S3 / GCS /
	// R2 will reject an http:// connection with credentials in the URL
	// fragment.
	Secure bool `yaml:"secure"`
}

// AuthConfig configures auth.
type AuthConfig struct {
	JWTSecret        string `yaml:"jwt_secret"`
	UserTokenTTLSec  int    `yaml:"user_token_ttl_seconds"`
	AdminTokenTTLSec int    `yaml:"admin_token_ttl_seconds"`
	PasswordAlgo     string `yaml:"password_algo"` // argon2id (default)
}

// LogConfig configures logging.
type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// AdminInitSecret returns the bootstrap secret for /admin/init.
// In dev mode defaults to "dev-mode" so first-time setup just works.
// Production deployments MUST set PULSE_ADMIN_SECRET.
func (c *Config) AdminInitSecret() string {
	if v := os.Getenv("PULSE_ADMIN_SECRET"); v != "" {
		return v
	}
	return "dev-mode"
}

// APIConfig configures the REST API.
type APIConfig struct {
	Listen  string `yaml:"listen"` // ":32011"
	BaseURL string `yaml:"base_url"`
}

// GWConfig configures the WebSocket gateway.
type GWConfig struct {
	Listen         string `yaml:"listen"`       // ":32001"
	AdminListen    string `yaml:"admin_listen"` // ":32003"
	Subprotocol    string `yaml:"subprotocol"`  // pulse.protocol.v1.msgpack
	HeartbeatSec   int    `yaml:"heartbeat_seconds"`
	MaxConns       int    `yaml:"max_conns"`
	SessionTTLSec  int    `yaml:"session_ttl_seconds"`
	HandOffEnabled bool   `yaml:"handoff_enabled"`
}

// LicenseConfig configures the license service.
type LicenseConfig struct {
	Mode        string `yaml:"mode"` // self-hosted | cloud
	Server      string `yaml:"server"`
	CacheTTLSec int    `yaml:"cache_ttl_seconds"`
}

// Defaults returns a config suitable for local dev.
func Defaults() *Config {
	return &Config{
		Version: "dev",
		Cell: Cell{
			ID:            "cell-0",
			Region:        "local",
			UserShardFrom: 0,
			UserShardTo:   100000,
		},
		Roles: Roles{
			Cell:    true,
			Gateway: true,
			Storage: true,
			License: true,
			Admin:   true,
		},
		Postgres: PostgresConfig{
			Host:     "localhost",
			Port:     32432,
			Database: "pulse",
			User:     "pulse",
			Password: "pulse",
			SSLMode:  "disable",
			MaxConns: 20,
			MinConns: 2,
		},
		NATS: NATSConfig{
			URL:          "nats://localhost:32222",
			ClusterID:    "pulse-cluster",
			StreamPrefix: "pulse",
		},
		ObjectS3: S3Config{
			Backend:   "s3",
			Endpoint:  "localhost:32900",
			Region:    "us-east-1",
			Bucket:    "pulse-media",
			AccessKey: "pulse",
			SecretKey: "pulsepulse",
			PublicURL: "http://localhost:32900/pulse-media",
		},
		Auth: AuthConfig{
			JWTSecret:        "dev-secret-change-me-in-production-32chars",
			UserTokenTTLSec:  86400 * 30,
			AdminTokenTTLSec: 86400 * 365,
			PasswordAlgo:     "argon2id",
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
		},
		API: APIConfig{
			Listen:  ":32011",
			BaseURL: "http://localhost:32011",
		},
		GW: GWConfig{
			Listen:         ":32001",
			AdminListen:    ":32003",
			Subprotocol:    "pulse.protocol.v1.msgpack",
			HeartbeatSec:   10,
			MaxConns:       50000,
			SessionTTLSec:  86400 * 30,
			HandOffEnabled: true,
		},
		License: LicenseConfig{
			Mode:        "self-hosted",
			CacheTTLSec: 3600,
		},
	}
}

// Load reads YAML from path and applies defaults for missing fields.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Defaults()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.applyDefaults(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// LoadFromBytes parses config from in-memory bytes.
func LoadFromBytes(data []byte) (*Config, error) {
	cfg := Defaults()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if err := cfg.applyDefaults(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() error {
	d := Defaults()
	if c.Cell.ID == "" {
		c.Cell.ID = d.Cell.ID
	}
	if c.Postgres.Host == "" {
		c.Postgres = d.Postgres
	}
	if c.NATS.URL == "" {
		c.NATS = d.NATS
	}
	if c.ObjectS3.Endpoint == "" {
		c.ObjectS3 = d.ObjectS3
	}
	// PULSE_CELL_PEERS — comma-separated list of peer cell IDs
	// (deployment-orchestrator friendly). Overrides c.Cell.Peers
	// only when the env var is set; an empty c.Cell.Peers stays
	// empty (single-cell deployment).
	if v := os.Getenv("PULSE_CELL_PEERS"); v != "" {
		var peers []string
		for _, p := range strings.Split(v, ",") {
			if t := strings.TrimSpace(p); t != "" {
				peers = append(peers, t)
			}
		}
		c.Cell.Peers = peers
	}
	if v := os.Getenv("PULSE_JWT_SECRET"); v != "" {
		c.Auth.JWTSecret = v
	}
	if c.Auth.JWTSecret == "" {
		c.Auth.JWTSecret = d.Auth.JWTSecret
	}
	if c.Log.Level == "" {
		c.Log.Level = d.Log.Level
	}

	// In production mode (PULSE_DEV_MODE != "1"), require real secrets.
	// We refuse to start if any sensitive default is in use, to prevent
	// a misconfigured deployment from running with known secrets.
	if os.Getenv("PULSE_DEV_MODE") != "1" {
		const minSecretLen = 32
		if c.Auth.JWTSecret == d.Auth.JWTSecret {
			return fmt.Errorf("config: PULSE_JWT_SECRET must be set in production (refusing to start with dev default)")
		}
		if len(c.Auth.JWTSecret) < minSecretLen {
			return fmt.Errorf("config: PULSE_JWT_SECRET must be at least %d characters in production", minSecretLen)
		}
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.GW.Subprotocol == "" {
		c.GW.Subprotocol = "pulse.protocol.v1.msgpack"
	}
	return nil
}

// RolesList returns the active roles as strings.
func (c *Config) RolesList() []string {
	var roles []string
	if c.Roles.Cell {
		roles = append(roles, "cell")
	}
	if c.Roles.Gateway {
		roles = append(roles, "gateway")
	}
	if c.Roles.Storage {
		roles = append(roles, "storage")
	}
	if c.Roles.License {
		roles = append(roles, "license")
	}
	if c.Roles.Admin {
		roles = append(roles, "admin")
	}
	if c.Roles.Backup {
		roles = append(roles, "backup")
	}
	return roles
}

// String returns a safe redacted representation for logging.
func (c *Config) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cell=%s region=%s roles=%v\n", c.Cell.ID, c.Cell.Region, c.RolesList())
	fmt.Fprintf(&b, "postgres=%s:%d/%s\n", c.Postgres.Host, c.Postgres.Port, c.Postgres.Database)
	fmt.Fprintf(&b, "nats=%s\n", c.NATS.URL)
	fmt.Fprintf(&b, "object=%s bucket=%s\n", c.ObjectS3.Backend, c.ObjectS3.Bucket)
	fmt.Fprintf(&b, "gateway_listen=%s api_listen=%s\n", c.GW.Listen, c.API.Listen)
	return b.String()
}
