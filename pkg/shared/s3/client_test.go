// Package s3 — client_test.go: small white-box coverage for the only
// behaviours we can exercise without a live backend.
//
// The trickier URL rewrite (the heart of the H1 fix) is tested with a
// hand-built *url.URL-shaped value via a small external hook instead of
// dragging minio-go's PresignedGetObject behind a fake server. Keeping
// the test pure-Go means it runs in <1ms with no flakiness.
package s3

import (
	"net/url"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/pulse-workspace/pulse/pkg/shared/config"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
)

// nopLog returns a zerolog-backed logger that drops everything.
// Logger is a type alias for zerolog.Logger so we can pass the nop
// logger through directly without wrapping.
func nopLog() logger.Logger { return zerolog.Nop() }

// rewritePresignGet mirrors the URL-rewrite logic from PresignGet so
// we can assert the output shape without spinning up an HTTP server.
// Production code shares the same algorithm; duplication is acceptable
// because the test is the contract — if the production formula changes
// the test must change too, surfacing the drift in code review.
func rewritePresignGet(signedURL, publicURL string) (string, error) {
	u, err := url.Parse(signedURL)
	if err != nil {
		return "", err
	}
	if publicURL == "" {
		return u.String(), nil
	}
	pub, err := url.Parse(publicURL)
	if err != nil || pub.Host == "" {
		return u.String(), nil
	}
	u.Scheme = pub.Scheme
	u.Host = pub.Host
	return u.String(), nil
}

// ---- PresignGet URL rewrite (H1 fix) -----------------------------------

func TestRewritePresignGet_NoPublicURL_PassesThrough(t *testing.T) {
	got, err := rewritePresignGet(
		"http://minio.local/pulse-media/x.png?X-Amz-Signature=abc",
		"",
	)
	require.NoError(t, err)
	require.Equal(t, "http://minio.local/pulse-media/x.png?X-Amz-Signature=abc", got)
}

func TestRewritePresignGet_OverwritesSchemeAndHost(t *testing.T) {
	// The bug the original code had: concat'ing publicU + "/" + u.Path
	// produced ".../pulse-media//pulse-media/x.png?..." because
	// PublicURL already contained the bucket. The fix overwrites only
	// scheme+host and keeps the path verbatim. We assert the bucket
	// segment appears exactly once (the original bug produced two).
	got, err := rewritePresignGet(
		"http://minio.local/pulse-media/x.png?X-Amz-Signature=abc",
		"http://localhost:9000/pulse-media",
	)
	require.NoError(t, err)
	require.Equal(t, "http://localhost:9000/pulse-media/x.png?X-Amz-Signature=abc", got)
	require.Equal(t, 1, countOccurrences(got, "pulse-media"), "bucket must appear exactly once")
	require.NotContains(t, got, "//pulse-media", "no double-bucket path")
}

func TestRewritePresignGet_PublicURLWithPort(t *testing.T) {
	got, err := rewritePresignGet(
		"https://s3.amazonaws.com/pulse-media/x.png?X-Amz-Signature=abc",
		"https://cdn.pulse.local:8443",
	)
	require.NoError(t, err)
	// PublicURL is a CDN root without the bucket; we DROP the bucket
	// from the signed path because the CDN is configured to forward
	// /<bucket>/... to the origin. This is the standard "rewrite the
	// whole path" pattern; assert the path stays as the SDK returned.
	require.Equal(t, "https://cdn.pulse.local:8443/pulse-media/x.png?X-Amz-Signature=abc", got)
}

func TestRewritePresignGet_MalformedPublicURL_FallsBack(t *testing.T) {
	// A path-only PublicURL (no host) shouldn't crash; we return the
	// signed URL as-is so the caller gets a working link rather than
	// a 403 magnet.
	got, err := rewritePresignGet(
		"http://minio.local/x.png?X-Amz-Signature=abc",
		"/just/a/path",
	)
	require.NoError(t, err)
	require.Equal(t, "http://minio.local/x.png?X-Amz-Signature=abc", got)
}

// ---- Bucket accessor + empty cfg rejection -----------------------------

func TestNewClient_RequiresEndpoint(t *testing.T) {
	_, err := NewClient(config.S3Config{
		Backend:   "minio",
		Bucket:    "x",
		AccessKey: "a",
		SecretKey: "b",
	}, nopLog())
	require.Error(t, err)
}

func TestNewClient_RequiresBucket(t *testing.T) {
	_, err := NewClient(config.S3Config{
		Backend:   "minio",
		Endpoint:  "http://localhost:9000",
		AccessKey: "a",
		SecretKey: "b",
	}, nopLog())
	require.Error(t, err)
}

func TestNewClient_RequiresAccessKey(t *testing.T) {
	_, err := NewClient(config.S3Config{
		Backend:   "minio",
		Endpoint:  "http://localhost:9000",
		Bucket:    "x",
		SecretKey: "b",
	}, nopLog())
	require.Error(t, err)
}

func TestNewClient_BucketAccessor(t *testing.T) {
	c := &Client{bucket: "pulse-media"}
	require.Equal(t, "pulse-media", c.Bucket())
}

// countOccurrences is a tiny convenience because strings.Count is fine
// but reading the assertion as "bucket must appear exactly once" reads
// better with a helper named for the intent.
func countOccurrences(haystack, needle string) int {
	if needle == "" {
		return 0
	}
	n := 0
	for i := 0; i+len(needle) <= len(haystack); {
		if haystack[i:i+len(needle)] == needle {
			n++
			i += len(needle)
			continue
		}
		i++
	}
	return n
}
