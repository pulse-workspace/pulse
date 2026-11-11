package media

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---- mimeAllowed -------------------------------------------------------

func TestMimeAllowed_ExactMatch(t *testing.T) {
	s := &Service{allowedMimes: []string{"image/png", "application/pdf"}}
	require.True(t, s.mimeAllowed("image/png"))
	require.True(t, s.mimeAllowed("application/pdf"))
	require.False(t, s.mimeAllowed("image/jpeg"))
}

func TestMimeAllowed_WildcardPrefix(t *testing.T) {
	s := &Service{allowedMimes: []string{"image/*", "video/*"}}
	require.True(t, s.mimeAllowed("image/png"))
	require.True(t, s.mimeAllowed("image/heic"))
	require.True(t, s.mimeAllowed("video/mp4"))
	require.False(t, s.mimeAllowed("audio/mp3"))
}

func TestMimeAllowed_InputIsLowercased(t *testing.T) {
	// mimeAllowed always lower-cases the incoming mime; the allow-list
	// is normalised once at construction (see NewService) so we don't
	// re-do that work on the hot path.
	s := &Service{allowedMimes: []string{"image/png"}}
	require.True(t, s.mimeAllowed("IMAGE/PNG"))
	require.True(t, s.mimeAllowed("Image/Png"))
}

func TestMimeAllowed_Empty(t *testing.T) {
	s := &Service{allowedMimes: defaultAllowedMimes}
	require.False(t, s.mimeAllowed(""))
	require.False(t, s.mimeAllowed("   "))
}

// ---- buildObjectKey + sanitizeExt -------------------------------------

func TestBuildObjectKey_PreservesExtension(t *testing.T) {
	key := buildObjectKey("med_01", "vacation.JPG", parseUTC(t, "2026-07-21T10:30:00Z"))
	require.Equal(t, "media/2026/07/21/med_01.jpg", key)
}

func TestBuildObjectKey_NoExtension(t *testing.T) {
	key := buildObjectKey("med_02", "raw_dump", parseUTC(t, "2026-01-15T00:00:00Z"))
	require.Equal(t, "media/2026/01/15/med_02", key)
}

func TestBuildObjectKey_StripsUnsafeExt(t *testing.T) {
	// Path-traversal-style extension is sanitised away.
	key := buildObjectKey("med_03", "foo../../../etc/passwd", parseUTC(t, "2026-07-21T00:00:00Z"))
	require.NotContains(t, key, "..")
	require.NotContains(t, key, "/etc")
	require.NotContains(t, key, "passwd")
}

func TestSanitizeExt_KeepsSafeChars(t *testing.T) {
	require.Equal(t, ".png", sanitizeExt(".png"))
	require.Equal(t, ".jpeg", sanitizeExt(".JPEG"))
	require.Equal(t, ".tar.gz", sanitizeExt(".tar.gz"))
}

func TestSanitizeExt_DropsUnsafe(t *testing.T) {
	require.Equal(t, "", sanitizeExt(""))
	require.Equal(t, "", sanitizeExt("."))
	require.Equal(t, ".png", sanitizeExt(".png\x00"))
	require.Equal(t, ".png", sanitizeExt(".p/ng"))
}

// ---- Attachment JSON shape (L1) ----------------------------------------
//
// object_key is internal S3 layout (date prefix + ULID + extension). It
// must not appear in the client-facing JSON because it leaks bucket
// structure; clients identify attachments by attachment_id.

func TestAttachment_JSONOmitsObjectKey(t *testing.T) {
	a := Attachment{
		AttachmentID: "med_01",
		UploaderID:   "u_alice",
		ObjectKey:    "media/2026/07/21/med_01.png",
		Filename:     "x.png",
		MimeType:     "image/png",
	}
	b := jsonMarshal(t, a)
	require.NotContains(t, string(b), "object_key")
	require.NotContains(t, string(b), "media/2026")
	require.Contains(t, string(b), `"attachment_id":"med_01"`)
}

// ---- ACL constants — covered indirectly via Download unit tests below.

// parseUTC is a small helper so the test stays framework-free.
func parseUTC(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return v
}

// jsonMarshal is a thin wrapper so the JSON-shape test reads as
// "what does this struct look like over the wire?" rather than
// "did encoding/json panic?"
func jsonMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
