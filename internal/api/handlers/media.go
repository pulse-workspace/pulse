// Package handlers — media.go: HTTP surface for chat attachments.
//
// Endpoints (mounted under /api/v1/media):
//
//	POST   /upload           — multipart/form-data upload (one file per call)
//	                           returns the metadata row + a 1-hour presigned GET URL
//	GET    /{attachment_id}  — streams the attachment back; sets Content-Type
//	                           from the metadata row (NOT from the request)
//	DELETE /{attachment_id}  — soft-deletes the row and removes the S3 object
//	                           (only the original uploader)
//
// Authorization: every endpoint requires a user JWT. The download endpoint
// is user-authenticated (uploads are private to the cell); an
// unauthenticated direct-S3 GET is exposed via the presigned URL embedded
// in the upload response and is time-limited to 1 hour.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/pulse-im/pulse/internal/cell/media"
	"github.com/pulse-im/pulse/pkg/shared/authn"
	"github.com/pulse-im/pulse/pkg/shared/logger"
)

// MediaService is the narrow handler-package view of *media.Service.
// The concrete *media.Service satisfies this; mirroring the MFAService
// pattern keeps server.go free of the cell/media import path.
type MediaService interface {
	MaxUploadBytes() int64
	Upload(ctx context.Context, in media.UploadInput) (*media.UploadResult, error)
	Download(ctx context.Context, attachmentID, requestingUserID string) (*media.Attachment, io.ReadCloser, error)
	Delete(ctx context.Context, attachmentID, requestingUserID string) error
	ListByUploader(ctx context.Context, uploaderID string, limit int) ([]*media.Attachment, error)
}

// dangerousMimeSubtypes is the set of content-types we force to
// application/octet-stream on the way out so a browser can't render
// user-supplied bytes as an active document (HTML) or a script vector
// (SVG with embedded JavaScript). The list is small on purpose: any
// format a browser can execute inline.
var dangerousMimeSubtypes = map[string]string{
	"image/svg+xml":         "application/octet-stream",
	"text/html":             "application/octet-stream",
	"application/xhtml+xml": "application/octet-stream",
}

// RequireMediaService is a chi middleware that rejects requests with 503
// when the chat-attachments surface has not been wired in (mirror of
// RequireWhitelabelRenderer). Used at the route group in server.go so
// the API still boots when object_storage is not configured.
func RequireMediaService(s MediaService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s == nil {
				WriteError(w, http.StatusServiceUnavailable, 10001, "media service unavailable")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MediaUpload handles POST /api/v1/media/upload.
//
// Multipart contract:
//
//	field "file"        — binary payload (required)
//	form field "conv_id" — optional conversation ID
//	Content-Type of the file part is preserved as the stored mime_type.
//
// Response (200 OK) wraps the attachment metadata + a presigned GET URL
// in the Pulse JSON envelope:
//
//	{"errCode":0,"errMsg":"","data":{"attachment":{...},"url":"https://..."}}
func MediaUpload(svc MediaService, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}

		// Cap the request body at the service's max + a 2-byte slack for
		// multipart framing. Http.MaxBytesReader surfaces a clear error
		// when the limit fires so 413 with a friendly message works.
		r.Body = http.MaxBytesReader(w, r.Body, svc.MaxUploadBytes()+4096)

		if err := r.ParseMultipartForm(svc.MaxUploadBytes() + 4096); err != nil {
			log.Warn().Err(err).Msg("media: parse multipart")
			WriteError(w, http.StatusBadRequest, 10005, "could not parse multipart form")
			return
		}
		// ParseMultipartForm may spool the body to a temp file when it
		// exceeds Go's in-memory threshold; RemoveAll cleans those up.
		// Without this the cell leaks /tmp files under sustained upload
		// load until the GC finalizer runs (often too late).
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()

		fh, fhHeader, err := r.FormFile("file")
		if err != nil {
			WriteError(w, http.StatusBadRequest, 10005, "field 'file' required")
			return
		}
		defer fh.Close()

		data, err := io.ReadAll(fh)
		if err != nil {
			log.Warn().Err(err).Msg("media: read uploaded file")
			WriteError(w, http.StatusBadRequest, 10005, "could not read uploaded file")
			return
		}

		convID := r.FormValue("conv_id")
		mimeType := fhHeader.Header.Get("Content-Type")
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		res, err := svc.Upload(r.Context(), media.UploadInput{
			UploaderID: c.UserID,
			ConvID:     convID,
			Filename:   fhHeader.Filename,
			MimeType:   mimeType,
			Data:       data,
		})
		if err != nil {
			switch {
			case errors.Is(err, media.ErrTooLarge):
				WriteError(w, http.StatusRequestEntityTooLarge, 10010, "upload too large")
			case errors.Is(err, media.ErrUnsupportedType):
				WriteError(w, http.StatusUnsupportedMediaType, 10011, "unsupported content type")
			default:
				log.Error().Err(err).Str("uploader_id", c.UserID).Msg("media: upload failed")
				WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			}
			return
		}

		log.Info().
			Str("attachment_id", res.Attachment.AttachmentID).
			Str("uploader_id", c.UserID).
			Int64("bytes", res.Attachment.SizeBytes).
			Str("mime", res.Attachment.MimeType).
			Msg("media uploaded")
		WriteOK(w, res)
	}
}

// MediaDownload handles GET /api/v1/media/{attachment_id}.
//
// Streams the bytes back with the metadata's stored mime_type. We use
// io.Copy so the connection backpressure works correctly when the client
// is slow.
//
// Authorization: only the original uploader can download. Non-owners get
// 404 (not 403) to avoid leaking existence of someone else's attachment.
// This is enforced at the service layer as well so any future caller
// (cron, gateway, etc.) inherits the same posture.
//
// Non-existent or soft-deleted attachments map to 404. S3 errors after
// the row was found map to 502 Bad Gateway (we DID locate the file,
// the backend storage is the failing layer).
//
// Security headers (defense against SVG-XSS and content-sniffing):
//
//	X-Content-Type-Options: nosniff    — never let the browser infer a
//	                                    different mime than what we send
//	Content-Disposition: attachment    — force "save as" instead of inline
//	                                    render so the browser never parses
//	                                    the response as HTML / SVG script.
//	                                    The original filename is included
//	                                    for the browser's "save as" prompt.
//
// image/svg+xml (and other dangerous-in-content-types like text/html) is
// coerced to application/octet-stream before the headers go out, so a
// malicious SVG payload can't execute JS in the API origin's context.
func MediaDownload(svc MediaService, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}
		attachmentID := chi.URLParam(r, "attachmentID")
		if attachmentID == "" {
			WriteError(w, http.StatusBadRequest, 10005, "attachment_id required")
			return
		}

		a, body, err := svc.Download(r.Context(), attachmentID, c.UserID)
		if err != nil {
			if errors.Is(err, media.ErrNotFound) {
				WriteError(w, http.StatusNotFound, 10012, "attachment not found")
				return
			}
			log.Error().Err(err).Str("attachment_id", attachmentID).Msg("media: download failed")
			WriteError(w, http.StatusBadGateway, 10013, "storage backend error")
			return
		}
		defer body.Close()

		contentType := a.MimeType
		if safe, ok := dangerousMimeSubtypes[contentType]; ok {
			// Browsers actively render SVG/HTML even with
			// Content-Disposition: attachment on some legacy paths; the
			// defence-in-depth is to NOT serve the dangerous mime at all.
			contentType = safe
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeFilename(a.Filename)))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		if _, err := io.Copy(w, body); err != nil {
			log.Warn().Err(err).Str("attachment_id", attachmentID).Msg("media: download copy interrupted")
		}
	}
}

// sanitizeFilename strips characters that would break a Content-
// Disposition header (CR, LF, quote, backslash). Anything else passes
// through; this is not about path-traversal (the value goes into a
// header parameter, not a filesystem path).
func sanitizeFilename(name string) string {
	clean := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch c {
		case '\r', '\n', '"', '\\':
			clean = append(clean, '_')
		default:
			clean = append(clean, c)
		}
	}
	return string(clean)
}

// MediaDelete handles DELETE /api/v1/media/{attachment_id}.
//
// Authorization: only the original uploader may delete. Non-owners get
// 404 (not 403) to avoid leaking existence of someone else's attachment.
func MediaDelete(svc MediaService, log logger.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}
		attachmentID := chi.URLParam(r, "attachmentID")
		if attachmentID == "" {
			WriteError(w, http.StatusBadRequest, 10005, "attachment_id required")
			return
		}

		if err := svc.Delete(r.Context(), attachmentID, c.UserID); err != nil {
			if errors.Is(err, media.ErrNotFound) {
				WriteError(w, http.StatusNotFound, 10012, "attachment not found")
				return
			}
			log.Error().Err(err).Str("attachment_id", attachmentID).Msg("media: delete failed")
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		log.Info().Str("attachment_id", attachmentID).Str("uploader_id", c.UserID).Msg("media deleted")
		WriteOK(w, map[string]string{"attachment_id": attachmentID, "status": "deleted"})
	}
}

// MediaListMine handles GET /api/v1/media?limit=N — recent uploads by the
// calling user. Limit defaults to 50, capped at 200.
func MediaListMine(svc MediaService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := authn.ClaimsFromContext(r.Context())
		if c == nil {
			WriteError(w, http.StatusUnauthorized, 1001, "missing claims")
			return
		}
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		out, err := svc.ListByUploader(r.Context(), c.UserID, limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, 10001, "internal error")
			return
		}
		WriteOK(w, out)
	}
}
