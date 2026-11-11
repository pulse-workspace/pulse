// Package media owns chat attachments — images, video, audio, and generic
// files uploaded by users. The actual bytes live in S3-compatible object
// storage (MinIO in dev); this service persists metadata in Postgres and
// hands out signed URLs that the API layer can stream back to clients.
//
// File layout:
//   - types.go        — Attachment record + UploadInput / UploadResult DTOs
//   - persistence.go  — SQL helpers (load by id, list by uploader, soft-delete)
//   - service.go      — cell.Service implementation: lifecycle, Upload,
//     Download, Delete, PresignGet
//
// The package implements cell.Service so it boots/stops alongside the
// rest of the cell-role services in cmd/pulse/main.go.
package media

import (
	"errors"
	"time"
)

// Attachment is one row in media_attachments.
//
// Soft-delete is handled by the status column (status='deleted'). The
// underlying S3 object is removed asynchronously by DeleteAttachment;
// readers running between the soft-delete and the S3 cleanup still see
// a metadata row but the read endpoint rejects it via status.
//
// object_key is tagged json:"-" because it leaks the bucket layout
// (date prefix + ULID path) to clients and is an internal storage
// concern; clients use AttachmentID for the GET handler. The column is
// retained in the table for forensics + janitor work.
type Attachment struct {
	AttachmentID string    `json:"attachment_id"`
	UploaderID   string    `json:"uploader_id"`
	ConvID       *string   `json:"conv_id,omitempty"`
	ObjectKey    string    `json:"-"`
	Filename     string    `json:"filename"`
	MimeType     string    `json:"mime_type"`
	SizeBytes    int64     `json:"size_bytes"`
	SHA256Hex    string    `json:"sha256_hex,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UploadInput is the service-level DTO Upload expects. Fields mirror the
// multipart/form-data fields exposed by handlers.MediaUpload. Keep these
// in lock-step — handlers decode into UploadInput directly via JSON.
type UploadInput struct {
	UploaderID string
	ConvID     string // empty => not tied to a conversation
	Filename   string
	MimeType   string
	Data       []byte // pre-read bytes; the handler reads the multipart body
}

// UploadResult is what handlers.MediaUpload returns to clients.
type UploadResult struct {
	Attachment Attachment `json:"attachment"`
	URL        string     `json:"url"` // presigned GET URL (download, 1h expiry)
}

// ErrNotFound — the requested attachment does not exist or has been
// hard-deleted. Returned to API layer as 404.
var ErrNotFound = errors.New("media: attachment not found")

// ErrTooLarge — uploaded payload exceeds the configured maximum. Surface
// to API layer as 413 Payload Too Large with a retry hint.
var ErrTooLarge = errors.New("media: upload exceeds maximum size")

// ErrUnsupportedType — MimeType is not in the allow-list. Returns 415
// Unsupported Media Type from the API layer.
var ErrUnsupportedType = errors.New("media: unsupported content type")

// DefaultMaxUploadBytes is the per-cell hard cap applied when the
// deployer doesn't override media.max_upload_bytes in config. 50 MiB is
// generous enough for screenshots + short video clips while bounding
// memory pressure on the cell (the body is fully buffered before S3).
const DefaultMaxUploadBytes int64 = 50 << 20 // 50 MiB

// AllowedMimes is the default allow-list when no override is configured.
// Conservative by design; deployers running file-storage cells should
// override via media.allowed_mimes in config.
//
// Patterns are matched by exact equality against the upload's Content-Type
// header; the wildcards `image/*`, `video/*`, `audio/*` cover whole
// families so we don't have to enumerate every codec.
var defaultAllowedMimes = []string{
	"image/jpeg", "image/png", "image/gif", "image/webp", "image/svg+xml",
	"video/mp4", "video/webm", "video/quicktime",
	"audio/mpeg", "audio/ogg", "audio/wav", "audio/webm",
	"application/pdf",
	"application/zip", "application/x-tar", "application/gzip",
	"text/plain", "text/csv", "text/markdown",
}
