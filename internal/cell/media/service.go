// Package media — service.go: cell.Service implementation, upload/download/delete,
// and the S3 presign flow that lets clients push bytes directly to the bucket.
//
// The Service is constructed once per cell role via NewService in
// cmd/pulse/main.go. It implements cell.Service so it shuts down with the
// rest of the cell-role services; the underlying *s3.Client holds only HTTP
// keepalive connections, so Stop is a no-op.
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/pulse-workspace/pulse/internal/cell"
	"github.com/pulse-workspace/pulse/pkg/shared/db"
	"github.com/pulse-workspace/pulse/pkg/shared/logger"
	"github.com/pulse-workspace/pulse/pkg/shared/s3"
)

// Service is the media cell service. Implements cell.Service.
type Service struct {
	cell         *cell.Cell
	log          logger.Logger
	db           db.DBTX
	s3           *s3.Client
	maxBytes     int64
	allowedMimes []string
}

// NewService builds the media service. The S3 client is constructed by
// the caller and passed in so this package owns no S3 credentials.
//
// maxBytes == 0 falls back to DefaultMaxUploadBytes. allowedMimes == nil
// falls back to defaultAllowedMimes. Both are mutable field-by-field so
// future config flags can override without breaking the call site.
//
// allowedMimes are normalised to lowercase once at construction so the
// hot-path mimeAllowed check stays branch-free.
func NewService(c *cell.Cell, s3c *s3.Client, maxBytes int64, allowedMimes []string) *Service {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxUploadBytes
	}
	if len(allowedMimes) == 0 {
		allowedMimes = defaultAllowedMimes
	}
	lower := make([]string, len(allowedMimes))
	for i, m := range allowedMimes {
		lower[i] = strings.ToLower(strings.TrimSpace(m))
	}
	return &Service{
		cell:         c,
		log:          c.Log.With().Str("service", "media").Logger(),
		db:           c.DB,
		s3:           s3c,
		maxBytes:     maxBytes,
		allowedMimes: lower,
	}
}

// Name implements cell.Service.
func (s *Service) Name() string { return "media" }

// MaxUploadBytes returns the per-upload cap the API layer enforces on
// the multipart body. Exposed so handlers can arm http.MaxBytesReader
// without re-reading the config.
func (s *Service) MaxUploadBytes() int64 { return s.maxBytes }

// Start makes sure the configured S3 bucket exists. Idempotent; logs once.
func (s *Service) Start(ctx context.Context) error {
	if err := s.s3.EnsureBucket(ctx); err != nil {
		return fmt.Errorf("media: ensure bucket: %w", err)
	}
	s.log.Info().Int64("max_bytes", s.maxBytes).Int("allowed_mimes", len(s.allowedMimes)).Msg("media service started")
	return nil
}

// Stop is a no-op — the underlying *s3.Client releases its HTTP keepalive
// pool when the cell context is cancelled.
func (s *Service) Stop(ctx context.Context) error { return nil }

// ---- public API --------------------------------------------------------

// Upload is the synchronous upload path. The handler reads the multipart
// body into bytes, builds a UploadInput, and calls this. We stream into
// S3 via PutObject so memory pressure on the cell stays bounded.
//
// On success we insert the metadata row and return a presigned GET URL
// for downstream readers.
func (s *Service) Upload(ctx context.Context, in UploadInput) (*UploadResult, error) {
	if in.UploaderID == "" {
		return nil, errors.New("media: uploader_id required")
	}
	if in.Filename == "" {
		return nil, errors.New("media: filename required")
	}
	if in.MimeType == "" {
		in.MimeType = "application/octet-stream"
	}
	if !s.mimeAllowed(in.MimeType) {
		return nil, ErrUnsupportedType
	}
	if int64(len(in.Data)) > s.maxBytes {
		return nil, ErrTooLarge
	}

	// Build the storage key: media/YYYY/MM/DD/<ulid>.<ext> so per-day
	// lifecycle policies are possible without scanning every row.
	attachmentID := "med_" + ulid.Make().String()
	now := time.Now().UTC()
	key := buildObjectKey(attachmentID, in.Filename, now)

	// Stream to S3; bounds-check size first so we don't push an
	// unbounded body if the caller lied about the cap.
	sum := sha256.Sum256(in.Data)
	if _, err := s.s3.PutObject(ctx, key, in.MimeType, bytes.NewReader(in.Data), int64(len(in.Data))); err != nil {
		return nil, fmt.Errorf("media: s3 put: %w", err)
	}

	a := &Attachment{
		AttachmentID: attachmentID,
		UploaderID:   in.UploaderID,
		ObjectKey:    key,
		Filename:     in.Filename,
		MimeType:     in.MimeType,
		SizeBytes:    int64(len(in.Data)),
		SHA256Hex:    hex.EncodeToString(sum[:]),
		Status:       "ready",
	}
	if in.ConvID != "" {
		cid := in.ConvID
		a.ConvID = &cid
	}

	if err := s.insertAttachment(ctx, a); err != nil {
		// Best-effort cleanup of the S3 object we just wrote — without
		// it the bucket leaks an orphan per failed insert. We don't fail
		// the cleanup (the caller already has nothing); we just log so
		// operators can spot ongoing DB-hiccup orphans.
		if delErr := s.s3.DeleteObject(ctx, key); delErr != nil {
			s.log.Warn().Err(delErr).Str("attachment_id", attachmentID).Str("key", key).Msg("s3 cleanup after insert failure failed (orphan)")
		}
		return nil, fmt.Errorf("media: persist metadata: %w", err)
	}

	url, err := s.PresignGet(ctx, key, time.Hour)
	if err != nil {
		// Upload succeeded; presign failure is non-fatal — log + return
		// the row so the caller can list it manually if needed.
		s.log.Warn().Err(err).Str("attachment_id", attachmentID).Msg("presign after upload failed")
	}

	// Re-read so created_at/updated_at are populated.
	reread, err := s.loadByID(ctx, attachmentID)
	if err != nil {
		return nil, fmt.Errorf("media: reread after insert: %w", err)
	}

	return &UploadResult{
		Attachment: *reread,
		URL:        url,
	}, nil
}

// Download opens an attachment for streaming. Callers wrap the returned
// io.ReadCloser in their HTTP response writer and call Close when done.
// Returns ErrNotFound for non-existent or soft-deleted attachments.
//
// Authorization: only the original uploader can download (same posture
// as Delete). Future work (M3 in the code-review) will loosen this for
// conversation participants once a membership-lookup helper lives in
// internal/cell/conversation.
//
// The returned attachment row is the canonical source for Content-Type
// and the original filename — using S3's stat.ContentType instead would
// drift between MinIO's default (application/octet-stream) and the
// mime_type the uploader declared.
func (s *Service) Download(ctx context.Context, attachmentID, requestingUserID string) (*Attachment, io.ReadCloser, error) {
	a, err := s.loadByID(ctx, attachmentID)
	if err != nil {
		return nil, nil, err
	}
	// Defense-in-depth: ACL at the service layer, not just the handler.
	// Returns ErrNotFound (not ErrForbidden) so non-owners can't probe
	// for existence by toggling between 403/404.
	if a.UploaderID != requestingUserID {
		return nil, nil, ErrNotFound
	}
	body, _, _, err := s.s3.GetObject(ctx, a.ObjectKey)
	if err != nil {
		return nil, nil, fmt.Errorf("media: s3 get: %w", err)
	}
	return a, body, nil
}

// Delete soft-deletes the metadata row and removes the S3 object. The
// soft-delete is what gates reader visibility; the S3 cleanup is best-
// effort — failure to drop the object leaves a recoverable row that a
// janitor (future) can sweep.
//
// Authorization: only the original uploader may delete. Other callers
// get ErrNotFound to avoid leaking existence.
func (s *Service) Delete(ctx context.Context, attachmentID, requestingUserID string) error {
	a, err := s.softDelete(ctx, attachmentID, requestingUserID)
	if err != nil {
		return err
	}
	if err := s.s3.DeleteObject(ctx, a.ObjectKey); err != nil {
		s.log.Warn().Err(err).Str("attachment_id", attachmentID).Msg("s3 cleanup after soft-delete failed (janitor will retry)")
	}
	return nil
}

// PresignGet exposes the underlying *s3.Client.PresignGet for API-layer
// reuse (e.g. when returning a fresh URL for an already-uploaded file).
func (s *Service) PresignGet(ctx context.Context, objectKey string, expiresIn time.Duration) (string, error) {
	return s.s3.PresignGet(ctx, objectKey, expiresIn)
}

// ListByUploader delegates to the persistence helper so API-layer code
// doesn't have to import this package's unexported layer.
func (s *Service) ListByUploader(ctx context.Context, uploaderID string, limit int) ([]*Attachment, error) {
	return s.listByUploader(ctx, uploaderID, limit)
}

// ---- internal helpers --------------------------------------------------

// mimeAllowed matches an upload's Content-Type against the allow-list.
// Wildcards (`image/*`, `video/*`, `audio/*`) match the family.
func (s *Service) mimeAllowed(mt string) bool {
	mt = strings.ToLower(strings.TrimSpace(mt))
	if mt == "" {
		return false
	}
	for _, p := range s.allowedMimes {
		if base, ok := strings.CutSuffix(p, "/*"); ok {
			if strings.HasPrefix(mt, base+"/") {
				return true
			}
			continue
		}
		if p == mt {
			return true
		}
	}
	return false
}

// buildObjectKey scopes uploads under a per-day prefix. Layout:
//
//	media/YYYY/MM/DD/<attachment_id>.<ext>
//
// The original filename's extension is preserved (lowercased, ASCII
// only) so a download via the presigned URL gets a sensible
// Content-Disposition. ASCII-only sanitization avoids path-traversal
// attempts via Unicode lookalikes.
func buildObjectKey(attachmentID, filename string, now time.Time) string {
	ext := strings.ToLower(path.Ext(filename))
	ext = sanitizeExt(ext)
	day := now.Format("2006/01/02")
	if ext != "" {
		return fmt.Sprintf("media/%s/%s%s", day, attachmentID, ext)
	}
	return fmt.Sprintf("media/%s/%s", day, attachmentID)
}

// sanitizeExt keeps only bytes that are safe in S3 keys: [a-z0-9.].
// Uppercase A-Z is lower-cased before filtering so callers don't have
// to normalise. Any other char (path separators, NUL, Unicode look-
// alikes) is silently dropped. A leading-dot-only result returns "" so
// buildObjectKey never produces a trailing dot in the S3 key.
func sanitizeExt(ext string) string {
	if ext == "" || ext == "." {
		return ""
	}
	clean := make([]byte, 0, len(ext))
	for i := 0; i < len(ext); i++ {
		c := ext[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' {
			clean = append(clean, c)
		}
	}
	out := string(clean)
	if out == "" || out == "." {
		return ""
	}
	return out
}
