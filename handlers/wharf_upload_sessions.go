package handlers

import (
	"context"
	"denkit-stash/models"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gorilla/mux"
)

// POST /wharf/upload-sessions/{id} starts a deferred resumable upload session.
func (h *WharfHandlers) StartUploadSession(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["id"]
	session, err := h.db.GetUploadSessionByID(sessionID)
	if err != nil {
		http.Error(w, `{"errors":["upload session not found"]}`, http.StatusNotFound)
		return
	}
	if session.State == "" {
		session.State = "active"
		_ = h.db.UpdateUploadSession(session)
	}
	w.Header().Set("Location", h.absoluteURL(r, "/wharf/upload-sessions/"+session.ID))
	w.WriteHeader(http.StatusCreated)
}

// PUT /wharf/upload-sessions/{id} appends or finalizes bytes using GCS-style
// resumable Content-Range semantics, which is what butler's uploader expects.
func (h *WharfHandlers) PutUploadSession(w http.ResponseWriter, r *http.Request) {
	sessionID := mux.Vars(r)["id"]
	session, err := h.db.GetUploadSessionByID(sessionID)
	if err != nil {
		http.Error(w, `{"errors":["upload session not found"]}`, http.StatusNotFound)
		return
	}
	if session.State == "completed" {
		w.WriteHeader(http.StatusOK)
		return
	}

	contentRange := r.Header.Get("Content-Range")
	if contentRange == "" {
		contentRange = r.Header.Get("content-range")
	}
	if contentRange == "bytes */*" {
		h.writeResumeRange(w, session.Size)
		return
	}

	matches := contentRangePattern.FindStringSubmatch(contentRange)
	if matches == nil {
		emptyFinalMatches := emptyFinalRangePattern.FindStringSubmatch(contentRange)
		if emptyFinalMatches == nil {
			http.Error(w, `{"errors":["invalid content range"]}`, http.StatusBadRequest)
			return
		}
		start, _ := strconv.ParseInt(emptyFinalMatches[1], 10, 64)
		total, _ := strconv.ParseInt(emptyFinalMatches[2], 10, 64)
		if total > maxUploadSessionBytes() {
			http.Error(w, `{"errors":["upload session exceeds maximum size"]}`, http.StatusRequestEntityTooLarge)
			return
		}
		if start != session.Size || total != session.Size {
			h.writeResumeRange(w, session.Size)
			return
		}
		if err = h.commitUploadSession(r.Context(), session, h.uploadSessionPath(session.ID)); err != nil {
			http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	start, _ := strconv.ParseInt(matches[1], 10, 64)
	end, _ := strconv.ParseInt(matches[2], 10, 64)
	totalStr := matches[3]
	if start != session.Size {
		h.writeResumeRange(w, session.Size)
		return
	}
	if end < start {
		http.Error(w, `{"errors":["invalid content range"]}`, http.StatusBadRequest)
		return
	}
	chunkSize := end - start + 1
	if session.Size+chunkSize > maxUploadSessionBytes() {
		http.Error(w, `{"errors":["upload session exceeds maximum size"]}`, http.StatusRequestEntityTooLarge)
		return
	}
	var total int64
	if totalStr != "*" {
		total, _ = strconv.ParseInt(totalStr, 10, 64)
		if total > maxUploadSessionBytes() {
			http.Error(w, `{"errors":["upload session exceeds maximum size"]}`, http.StatusRequestEntityTooLarge)
			return
		}
		if start+chunkSize != total {
			http.Error(w, `{"errors":["final upload size mismatch"]}`, http.StatusBadRequest)
			return
		}
	}

	sessionPath := h.uploadSessionPath(session.ID)
	if err = os.MkdirAll(filepath.Dir(sessionPath), 0755); err != nil {
		http.Error(w, `{"errors":["could not prepare upload session"]}`, http.StatusInternalServerError)
		return
	}

	file, err := os.OpenFile(sessionPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		http.Error(w, `{"errors":["could not open upload session"]}`, http.StatusInternalServerError)
		return
	}
	if err = file.Truncate(session.Size); err != nil {
		_ = file.Close()
		http.Error(w, `{"errors":["could not prepare upload session"]}`, http.StatusInternalServerError)
		return
	}
	if _, err = file.Seek(session.Size, io.SeekStart); err != nil {
		_ = file.Close()
		http.Error(w, `{"errors":["could not prepare upload session"]}`, http.StatusInternalServerError)
		return
	}
	written, copyErr := io.Copy(file, io.LimitReader(r.Body, chunkSize))
	closeErr := file.Close()
	if copyErr != nil {
		http.Error(w, `{"errors":["could not write upload bytes"]}`, http.StatusInternalServerError)
		return
	}
	if closeErr != nil {
		http.Error(w, `{"errors":["could not close upload session"]}`, http.StatusInternalServerError)
		return
	}
	if written != chunkSize {
		http.Error(w, `{"errors":["content length does not match content range"]}`, http.StatusBadRequest)
		return
	}

	session.Size += written
	if totalStr == "*" {
		if err = h.db.UpdateUploadSession(session); err != nil {
			http.Error(w, `{"errors":["could not update upload session"]}`, http.StatusInternalServerError)
			return
		}
		h.writeResumeRange(w, session.Size)
		return
	}

	if session.Size != total {
		http.Error(w, `{"errors":["final upload size mismatch"]}`, http.StatusBadRequest)
		return
	}

	if err = h.commitUploadSession(r.Context(), session, sessionPath); err != nil {
		http.Error(w, fmt.Sprintf(`{"errors":["%s"]}`, err.Error()), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *WharfHandlers) uploadSessionPath(sessionID string) string {
	return filepath.Join("storage", "upload-sessions", sessionID)
}

func (h *WharfHandlers) writeResumeRange(w http.ResponseWriter, size int64) {
	if size > 0 {
		w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", size-1))
	}
	w.WriteHeader(308)
}

func (h *WharfHandlers) commitUploadSession(ctx context.Context, session *models.UploadSession, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("could not open completed upload: %w", err)
	}
	defer file.Close()

	if err = h.storage.Put(ctx, session.StoragePath, file, session.Size, "application/octet-stream"); err != nil {
		return fmt.Errorf("could not store completed upload: %w", err)
	}

	session.State = "completed"
	if err = h.db.UpdateUploadSession(session); err != nil {
		return fmt.Errorf("could not mark upload session completed: %w", err)
	}
	return nil
}
