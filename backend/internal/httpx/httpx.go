// Package httpx holds the JSON and SSE plumbing shared by HTTP handlers.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrorBody is the envelope of every error response:
//
//	{"error": {"code": "validation_failed", "message": "...", "details": [...]}}
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// StatusError is an error with an HTTP status and a stable machine code.
type StatusError struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *StatusError) Error() string { return e.Message }

func Errorf(status int, code, format string, args ...any) *StatusError {
	return &StatusError{Status: status, Code: code, Message: fmt.Sprintf(format, args...)}
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, err *StatusError) {
	WriteJSON(w, err.Status, ErrorBody{Error: ErrorDetail{Code: err.Code, Message: err.Message, Details: err.Details}})
}

// Decode reads a JSON body strictly: bounded size, unknown fields rejected,
// exactly one value.
func Decode(w http.ResponseWriter, r *http.Request, dst any) *StatusError {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		return Errorf(http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return Errorf(http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds %d bytes", tooLarge.Limit)
		}
		if errors.Is(err, io.EOF) {
			return Errorf(http.StatusBadRequest, "invalid_json", "request body is empty")
		}
		return Errorf(http.StatusBadRequest, "invalid_json", "invalid JSON body: %v", err)
	}
	if dec.More() {
		return Errorf(http.StatusBadRequest, "invalid_json", "request body must be a single JSON value")
	}
	return nil
}

// SSE writes Server-Sent Events.
type SSE struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func NewSSE(w http.ResponseWriter) *SSE {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // nginx: do not buffer the stream
	w.WriteHeader(http.StatusOK)
	s := &SSE{w: w, rc: http.NewResponseController(w)}
	_ = s.rc.Flush()
	return s
}

// Send writes one event and flushes it.
func (s *SSE) Send(event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b); err != nil {
		return err
	}
	return s.rc.Flush()
}

// Comment writes a keep-alive comment line.
func (s *SSE) Comment() error {
	if _, err := io.WriteString(s.w, ": ping\n\n"); err != nil {
		return err
	}
	return s.rc.Flush()
}
