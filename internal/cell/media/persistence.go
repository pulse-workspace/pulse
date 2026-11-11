// Package media — persistence.go: SQL helpers for the media_attachments table.
//
// All exports are methods on *Service so we can keep db.DBTX (an
// interface satisfied by both *db.DB and pgx.Tx) as the only field the
// helpers touch; no other layer needs direct DB access.
package media

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/pulse-im/pulse/pkg/shared/db"
)

// attachmentColumns is the canonical column order used in every scan.
// Kept as a const so it stays in lock-step with the SELECT-list below
// and the rowScanner helper at the bottom of this file.
const attachmentColumns = `attachment_id, uploader_id, conv_id, object_key,
	filename, mime_type, size_bytes, COALESCE(sha256_hex, ''), status,
	created_at, updated_at`

// rowScanner accepts a pgx.Row and decodes it into an Attachment. Used
// by both QueryRow + Rows scanners so the struct <-> column mapping
// lives in exactly one place.
func (s *Service) rowScanner(row pgx.Row) (*Attachment, error) {
	var a Attachment
	var convID *string
	if err := row.Scan(
		&a.AttachmentID, &a.UploaderID, &convID, &a.ObjectKey,
		&a.Filename, &a.MimeType, &a.SizeBytes, &a.SHA256Hex, &a.Status,
		&a.CreatedAt, &a.UpdatedAt,
	); err != nil {
		return nil, err
	}
	a.ConvID = convID
	return &a, nil
}

// insertAttachment writes a new row. The DB defaults (created_at,
// updated_at) are set by Postgres so the Go side never sees a clock skew.
func (s *Service) insertAttachment(ctx context.Context, a *Attachment) error {
	var convID any
	if a.ConvID != nil && *a.ConvID != "" {
		convID = *a.ConvID
	}
	return s.db.Exec(ctx, `
		INSERT INTO media_attachments
			(attachment_id, uploader_id, conv_id, object_key, filename,
			 mime_type, size_bytes, sha256_hex, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), 'ready')
	`, a.AttachmentID, a.UploaderID, convID, a.ObjectKey, a.Filename,
		a.MimeType, a.SizeBytes, a.SHA256Hex)
}

// loadByID looks up one row by its ULID. Returns ErrNotFound when the
// row doesn't exist OR has been soft-deleted (status='deleted'); both
// cases are indistinguishable to API callers.
func (s *Service) loadByID(ctx context.Context, attachmentID string) (*Attachment, error) {
	row := s.db.QueryRow(ctx, `
		SELECT `+attachmentColumns+`
		FROM media_attachments
		WHERE attachment_id = $1 AND status = 'ready'
		LIMIT 1
	`, attachmentID)
	a, err := s.rowScanner(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("media: load attachment: %w", err)
	}
	return a, nil
}

// listByUploader returns recent uploads by one user, newest first.
// Used by the future "my uploads" UI / quota enforcement endpoints.
func (s *Service) listByUploader(ctx context.Context, uploaderID string, limit int) ([]*Attachment, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+attachmentColumns+`
		FROM media_attachments
		WHERE uploader_id = $1 AND status = 'ready'
		ORDER BY created_at DESC
		LIMIT $2
	`, uploaderID, limit)
	if err != nil {
		return nil, fmt.Errorf("media: list uploader: %w", err)
	}
	defer rows.Close()

	var out []*Attachment
	for rows.Next() {
		a, err := s.rowScanner(rows)
		if err != nil {
			return nil, fmt.Errorf("media: scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// softDelete flips status to 'deleted' and returns the row so the caller
// can drop the S3 object with the right key. The UPDATE is idempotent
// (re-running on an already-deleted row returns ErrNotFound so the
// caller doesn't waste an S3 round-trip).
func (s *Service) softDelete(ctx context.Context, attachmentID, requestingUserID string) (*Attachment, error) {
	a, err := s.loadByID(ctx, attachmentID)
	if err != nil {
		return nil, err
	}
	if a.UploaderID != requestingUserID {
		return nil, ErrNotFound // don't leak existence to non-owners
	}
	if err := s.db.Exec(ctx, `
		UPDATE media_attachments
		SET status = 'deleted'
		WHERE attachment_id = $1 AND status = 'ready'
	`, attachmentID); err != nil {
		return nil, fmt.Errorf("media: soft-delete: %w", err)
	}
	a.Status = "deleted"
	a.UpdatedAt = time.Now().UTC()
	return a, nil
}

// compile-time assertion that *Service uses db.DBTX in a way the
// package's tests can swap for a transaction. *db.DB satisfies
// db.DBTX so production code needs nothing else.
var _ db.DBTX = (*db.DB)(nil)
