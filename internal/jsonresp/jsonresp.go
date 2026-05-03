// Package jsonresp provides JSON response helpers and a consistent error
// envelope for HTTP handlers.
//
//	// Success
//	jsonresp.Write(w, http.StatusOK, user)
//
//	// Error with structured envelope
//	jsonresp.WriteError(w, r, http.StatusNotFound, "user.not_found", "no user with that id")
//
//	// Decode + size limit + reject unknown fields
//	var body CreateUser
//	if err := jsonresp.Decode(r, &body, jsonresp.MaxBytes(1<<20)); err != nil {
//	    jsonresp.WriteDecodeError(w, r, err)
//	    return
//	}
//
// The error envelope is `{ "error": { "code", "message", "requestId", "details" } }`.
// `requestId` is read from the request context via internal/httpmw.
package jsonresp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kaeawc/golang-build/internal/httpmw"
)

// ContentType is the value sent on every response.
const ContentType = "application/json; charset=utf-8"

// DefaultMaxRequestBytes bounds Decode by default. Override with MaxBytes
// per call.
const DefaultMaxRequestBytes int64 = 1 << 20 // 1 MiB

// Write encodes body as JSON and sends it with the given status code. If
// encoding fails the response is left in whatever state http.ResponseWriter
// is in (status may already be flushed); the encode error is returned to
// the caller for logging.
func Write(w http.ResponseWriter, status int, body any) error {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	if err := enc.Encode(body); err != nil {
		return fmt.Errorf("jsonresp: encode: %w", err)
	}
	return nil
}

// WriteRaw writes pre-encoded JSON bytes with the given status. Useful for
// cached or streamed payloads.
func WriteRaw(w http.ResponseWriter, status int, body []byte) error {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("jsonresp: write: %w", err)
	}
	return nil
}

// ErrorBody is the envelope used by WriteError. JSON form:
//
//	{ "error": { "code": "...", "message": "...", "requestId": "...", "details": {...} } }
type ErrorBody struct {
	Error ErrorEnvelope `json:"error"`
}

// ErrorEnvelope is the inner error object.
type ErrorEnvelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId,omitempty"`
	Details   any    `json:"details,omitempty"`
}

// WriteError writes a structured error envelope. requestId is taken from
// the request context if RequestID middleware is in the chain.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) error {
	return WriteErrorDetails(w, r, status, code, message, nil)
}

// WriteErrorDetails writes a structured error envelope with arbitrary
// JSON-serializable details (e.g. validation errors per field).
func WriteErrorDetails(w http.ResponseWriter, r *http.Request, status int, code, message string, details any) error {
	body := ErrorBody{Error: ErrorEnvelope{
		Code:      code,
		Message:   message,
		RequestID: httpmw.RequestIDFrom(r),
		Details:   details,
	}}
	return Write(w, status, body)
}

// DecodeOption configures Decode.
type DecodeOption func(*decodeCfg)

type decodeCfg struct {
	maxBytes      int64
	allowUnknown  bool
	disallowEmpty bool
}

// MaxBytes overrides DefaultMaxRequestBytes for a single Decode call. Values
// <= 0 disable the limit (NOT recommended for public endpoints).
func MaxBytes(n int64) DecodeOption {
	return func(c *decodeCfg) { c.maxBytes = n }
}

// AllowUnknownFields permits unknown JSON fields. By default they are
// rejected — this catches typos in client code at the API boundary.
func AllowUnknownFields() DecodeOption {
	return func(c *decodeCfg) { c.allowUnknown = true }
}

// RequireBody causes Decode to error on an empty body (default: empty body
// decodes into the zero value of v).
func RequireBody() DecodeOption {
	return func(c *decodeCfg) { c.disallowEmpty = true }
}

// Decode reads JSON from r.Body into v. By default it bounds the body to
// DefaultMaxRequestBytes and rejects unknown fields. Returns a *DecodeError
// classifying common failure modes.
func Decode(r *http.Request, v any, opts ...DecodeOption) error {
	cfg := decodeCfg{maxBytes: DefaultMaxRequestBytes}
	for _, opt := range opts {
		opt(&cfg)
	}
	if r.Body == nil {
		if cfg.disallowEmpty {
			return &DecodeError{Kind: DecodeKindEmpty, msg: "request body is empty"}
		}
		return nil
	}

	body := r.Body
	if cfg.maxBytes > 0 {
		body = http.MaxBytesReader(nil, r.Body, cfg.maxBytes)
	}
	dec := json.NewDecoder(body)
	if !cfg.allowUnknown {
		dec.DisallowUnknownFields()
	}

	if err := dec.Decode(v); err != nil {
		return classifyDecodeError(err, cfg)
	}

	// Reject trailing JSON (multiple values in body).
	if dec.More() {
		return &DecodeError{Kind: DecodeKindTrailing, msg: "request body has more than one JSON value"}
	}
	return nil
}

// DecodeKind classifies a Decode failure for handlers that want to emit
// different error codes per category.
type DecodeKind int

const (
	// DecodeKindUnknown is the fallback for errors that don't match any
	// specific category.
	DecodeKindUnknown DecodeKind = iota
	// DecodeKindMalformed is malformed JSON syntax.
	DecodeKindMalformed
	// DecodeKindUnknownField is a JSON field not present in the target type.
	DecodeKindUnknownField
	// DecodeKindWrongType is a value of the wrong JSON type for the target field.
	DecodeKindWrongType
	// DecodeKindTooLarge is a body exceeding the configured maxBytes.
	DecodeKindTooLarge
	// DecodeKindEmpty is an empty body when RequireBody was set.
	DecodeKindEmpty
	// DecodeKindTrailing is more than one JSON value in the body.
	DecodeKindTrailing
)

// Stable string identifiers for each DecodeKind, exported so callers and
// tests share the source of truth (e.g. when composing error codes).
const (
	KindStringMalformed    = "malformed"
	KindStringUnknownField = "unknown_field"
	KindStringWrongType    = "wrong_type"
	KindStringTooLarge     = "too_large"
	KindStringEmpty        = "empty"
	KindStringTrailing     = "trailing"
	KindStringUnknown      = "unknown"
)

// String returns a stable identifier for the kind.
func (k DecodeKind) String() string {
	switch k {
	case DecodeKindMalformed:
		return KindStringMalformed
	case DecodeKindUnknownField:
		return KindStringUnknownField
	case DecodeKindWrongType:
		return KindStringWrongType
	case DecodeKindTooLarge:
		return KindStringTooLarge
	case DecodeKindEmpty:
		return KindStringEmpty
	case DecodeKindTrailing:
		return KindStringTrailing
	default:
		return KindStringUnknown
	}
}

// DecodeError is the error type returned by Decode. Callers can switch on
// Kind to emit appropriate HTTP status codes / error envelopes.
type DecodeError struct {
	Kind DecodeKind
	msg  string
	err  error
}

func (e *DecodeError) Error() string { return e.msg }
func (e *DecodeError) Unwrap() error { return e.err }

func classifyDecodeError(err error, cfg decodeCfg) *DecodeError {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return &DecodeError{Kind: DecodeKindMalformed, msg: "malformed JSON: " + err.Error(), err: err}
	}
	var unmarshalErr *json.UnmarshalTypeError
	if errors.As(err, &unmarshalErr) {
		return &DecodeError{
			Kind: DecodeKindWrongType,
			msg:  fmt.Sprintf("wrong type for field %q: expected %s", unmarshalErr.Field, unmarshalErr.Type),
			err:  err,
		}
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		kind := DecodeKindMalformed
		if cfg.disallowEmpty && errors.Is(err, io.EOF) {
			kind = DecodeKindEmpty
		}
		return &DecodeError{Kind: kind, msg: "incomplete JSON body", err: err}
	}
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return &DecodeError{Kind: DecodeKindTooLarge, msg: err.Error(), err: err}
	}
	// json.Decoder reports unknown fields with a stable prefix; there is no
	// typed error in stdlib for this case.
	if strings.HasPrefix(err.Error(), "json: unknown field ") {
		return &DecodeError{Kind: DecodeKindUnknownField, msg: err.Error(), err: err}
	}
	return &DecodeError{Kind: DecodeKindUnknown, msg: err.Error(), err: err}
}

// WriteDecodeError translates a *DecodeError into an appropriate HTTP
// status + error code via the standard envelope. Other errors are written
// as 500 internal_error.
func WriteDecodeError(w http.ResponseWriter, r *http.Request, err error) error {
	var de *DecodeError
	if !errors.As(err, &de) {
		return WriteError(w, r, http.StatusInternalServerError, "internal_error", err.Error())
	}
	status, code := decodeStatusAndCode(de.Kind)
	return WriteError(w, r, status, code, de.msg)
}

func decodeStatusAndCode(k DecodeKind) (int, string) {
	switch k {
	case DecodeKindTooLarge:
		return http.StatusRequestEntityTooLarge, "request_too_large"
	case DecodeKindEmpty:
		return http.StatusBadRequest, "request_body_empty"
	default:
		return http.StatusBadRequest, "request_invalid"
	}
}
