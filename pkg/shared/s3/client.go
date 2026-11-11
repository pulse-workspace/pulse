// Package s3 is a thin facade over the minio-go client used by Pulse cells.
//
// We use minio-go because the OSS project's primary supported backend is
// MinIO (see configs/dev.yaml); minio-go is also a fully spec-compliant
// S3 client so a real S3 / GCS-interop / R2 deployment works without an
// additional translation layer. AWS SDK v2 is intentionally avoided —
// pulling it in for four operations would inflate the binary by ~40 MB
// for no behavioural gain.
//
// The facade exposes only the operations the cell needs:
//
//   - PutObject / GetObject : full backend round-trip for the upload +
//     download REST endpoints.
//   - PresignPut / PresignGet : time-limited signed URLs for clients that
//     want to bypass the cell (e.g. large-file
//     direct-to-S3 from a browser).
//
// Every method takes a context and surfaces backend errors wrapped with
// enough context for an operator to identify the failing call without
// jumping into a debugger.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/pulse-im/pulse/pkg/shared/config"
	"github.com/pulse-im/pulse/pkg/shared/logger"
)

// Client wraps *minio.Client with Pulse-specific knobs.
type Client struct {
	mc      *minio.Client
	cfg     config.S3Config
	log     logger.Logger
	bucket  string
	publicU string // pre-built PublicURL prefix (without trailing slash)
}

// NewClient constructs an S3-compatible client from the given config block.
//
// cfg.PublicURL is used verbatim as the public-facing scheme+host for
// PresignGet responses — this lets operators point browsers at a CDN
// edge while the cell still talks to the bucket via the private endpoint.
// Only the scheme + host + (optional) port are taken from PublicURL;
// the path, query, and signed headers come from the S3 SDK unchanged
// so the SigV4 signature remains valid.
func NewClient(cfg config.S3Config, log logger.Logger) (*Client, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" {
		return nil, errors.New("s3: endpoint and bucket are required")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("s3: access_key and secret_key are required")
	}

	endpoint := cfg.Endpoint
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
			endpoint = u.Host
		}
	}

	// Default Secure=true for real S3 / GCS / R2 deployments; minio can
	// override via cfg.Secure=false (the dev default in config.Defaults).
	// Production deployments MUST set Secure=true OR front the cell with
	// a TLS-terminating reverse proxy and accept the keys on the wire.
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.Secure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3: connect: %w", err)
	}

	return &Client{
		mc:      mc,
		cfg:     cfg,
		log:     log.With().Str("component", "s3").Str("backend", cfg.Backend).Logger(),
		bucket:  cfg.Bucket,
		publicU: trimSlash(cfg.PublicURL),
	}, nil
}

// EnsureBucket makes the configured bucket exist. Safe to call repeatedly
// (no-op when the bucket is already present). Failing fast at boot is the
// correct posture — without a bucket, every upload would 500 anyway.
func (c *Client) EnsureBucket(ctx context.Context) error {
	exists, err := c.mc.BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("s3: bucket check: %w", err)
	}
	if exists {
		return nil
	}
	if err := c.mc.MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{Region: c.cfg.Region}); err != nil {
		return fmt.Errorf("s3: make bucket: %w", err)
	}
	c.log.Info().Str("bucket", c.bucket).Msg("s3 bucket created")
	return nil
}

// PutObject streams `data` to the configured bucket under `key`. The
// contentType is sent as-is; callers should pass a meaningful value so
// browsers render the download correctly.
//
// Returns the etag + size reported by the backend so callers can persist
// them as metadata.
func (c *Client) PutObject(ctx context.Context, key, contentType string, data io.Reader, size int64) (etag string, err error) {
	info, err := c.mc.PutObject(ctx, c.bucket, key, data, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", fmt.Errorf("s3: put %q: %w", key, err)
	}
	return info.ETag, nil
}

// GetObject opens the object at `key` for reading. The returned
// io.ReadCloser MUST be closed by the caller; the underlying HTTP body
// is the connection that pays for the bytes.
//
// size > 0 is used to set Content-Length on the response; -1 means unknown.
func (c *Client) GetObject(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, "", 0, fmt.Errorf("s3: get %q: %w", key, err)
	}
	stat, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, "", 0, fmt.Errorf("s3: stat %q: %w", key, err)
	}
	return obj, stat.ContentType, stat.Size, nil
}

// DeleteObject removes a single key. Missing keys are NOT an error — the
// S3 spec treats DELETE on a missing key as idempotent (returns 204 even
// when the key did not exist). This matches what the cell's tombstone
// lifecycle expects when a soft-delete is replayed.
func (c *Client) DeleteObject(ctx context.Context, key string) error {
	if err := c.mc.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("s3: delete %q: %w", key, err)
	}
	return nil
}

// PresignGet returns a presigned GET URL the client can use to download a
// single object directly from S3. expiresIn caps the validity; values
// <= 0 fall back to 1h.
//
// The returned URL passes through cfg.PublicURL when set, but ONLY the
// scheme + host are rewritten — the path (which contains the bucket and
// object key) and the signed query string stay byte-identical to what
// minio-go handed back. Concat-ing publicU + "/" + u.Path double-buckets
// (PublicURL already includes the bucket) AND breaks SigV4 (the signed
// `host` header no longer matches), so we deliberately overwrite u's
// scheme/host instead of joining strings.
func (c *Client) PresignGet(ctx context.Context, key string, expiresIn time.Duration) (string, error) {
	if expiresIn <= 0 {
		expiresIn = time.Hour
	}
	reqParams := url.Values{}
	u, err := c.mc.PresignedGetObject(ctx, c.bucket, key, expiresIn, reqParams)
	if err != nil {
		return "", fmt.Errorf("s3: presign get %q: %w", key, err)
	}
	if c.publicU == "" {
		return u.String(), nil
	}
	pub, perr := url.Parse(c.publicU)
	if perr != nil || pub.Host == "" {
		// Malformed PublicURL — fall back to the signed URL rather
		// than returning a broken one (would 403 every download).
		return u.String(), nil
	}
	u.Scheme = pub.Scheme
	u.Host = pub.Host
	return u.String(), nil
}

// Bucket returns the configured bucket name. Used by callers building
// the public CDN fallback URL.
func (c *Client) Bucket() string { return c.bucket }

// trimSlash strips a trailing slash from URL prefixes so concatenation is
// safe in PresignGet.
func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
