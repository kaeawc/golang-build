package jsonresp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kaeawc/golang-build/internal/httpmw"
	"github.com/kaeawc/golang-build/internal/idgen"
)

func TestWriteSuccess(t *testing.T) {
	rec := httptest.NewRecorder()
	body := map[string]any{"name": "alice", "n": 42}
	if err := Write(rec, 201, body); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if rec.Code != 201 {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != ContentType {
		t.Errorf("Content-Type = %q, want %q", got, ContentType)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got["name"] != "alice" || got["n"] != float64(42) {
		t.Errorf("body = %v", got)
	}
}

func TestWriteRaw(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteRaw(rec, 200, []byte(`{"cached":true}`)); err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}
	if rec.Body.String() != `{"cached":true}` {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestWriteErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	if err := WriteError(rec, r, http.StatusNotFound, "user.not_found", "no such user"); err != nil {
		t.Fatalf("WriteError: %v", err)
	}
	if rec.Code != 404 {
		t.Errorf("status = %d", rec.Code)
	}
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if body.Error.Code != "user.not_found" || body.Error.Message != "no such user" {
		t.Errorf("envelope = %+v", body.Error)
	}
	if body.Error.RequestID != "" {
		t.Errorf("RequestID should be empty without middleware, got %q", body.Error.RequestID)
	}
}

func TestWriteErrorIncludesRequestID(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	// Run through RequestID middleware to populate the context.
	gen := idgen.NewSequence("rid")
	httpmw.RequestID(gen)(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = WriteError(w, req, 400, "bad_input", "invalid")
	})).ServeHTTP(rec, r)

	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, rec.Body.String())
	}
	if body.Error.RequestID != "rid-1" {
		t.Errorf("RequestID = %q, want rid-1", body.Error.RequestID)
	}
}

func TestWriteErrorDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	type fieldErr struct {
		Field string `json:"field"`
		Issue string `json:"issue"`
	}
	details := []fieldErr{
		{Field: "email", Issue: "must be valid email"},
		{Field: "age", Issue: "must be ≥ 18"},
	}
	_ = WriteErrorDetails(rec, r, 422, "validation_failed", "see details", details)

	var body struct {
		Error struct {
			Details []fieldErr `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(body.Error.Details) != 2 || body.Error.Details[0].Field != "email" {
		t.Errorf("details = %+v", body.Error.Details)
	}
}

func TestDecodeMaxBytes(t *testing.T) {
	type payload struct {
		X string `json:"x"`
	}
	huge := strings.Repeat("a", 200)
	body := []byte(`{"x":"` + huge + `"}`)
	r := httptest.NewRequest("POST", "/", bytes.NewReader(body))

	var v payload
	err := Decode(r, &v, MaxBytes(50))
	if err == nil {
		t.Fatal("expected too-large error")
	}
	var de *DecodeError
	if !errors.As(err, &de) {
		t.Fatalf("err = %T %v", err, err)
	}
	if de.Kind != DecodeKindTooLarge {
		t.Errorf("kind = %v, want TooLarge", de.Kind)
	}
}

func TestDecodeUnknownFieldRejected(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"alice","extra":"oops"}`))
	var v payload
	err := Decode(r, &v)
	if err == nil {
		t.Fatal("expected unknown-field error")
	}
	var de *DecodeError
	_ = errors.As(err, &de)
	if de.Kind != DecodeKindUnknownField {
		t.Errorf("kind = %v, want UnknownField", de.Kind)
	}
}

func TestDecodeAllowUnknown(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"alice","extra":"ok"}`))
	var v payload
	if err := Decode(r, &v, AllowUnknownFields()); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if v.Name != "alice" {
		t.Errorf("v = %+v", v)
	}
}

func TestDecodeMalformed(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"x":}`))
	var v map[string]any
	err := Decode(r, &v)
	var de *DecodeError
	_ = errors.As(err, &de)
	if de.Kind != DecodeKindMalformed {
		t.Errorf("kind = %v, want Malformed", de.Kind)
	}
}

func TestDecodeWrongType(t *testing.T) {
	type payload struct {
		Age int `json:"age"`
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"age":"not a number"}`))
	var v payload
	err := Decode(r, &v)
	var de *DecodeError
	_ = errors.As(err, &de)
	if de.Kind != DecodeKindWrongType {
		t.Errorf("kind = %v, want WrongType", de.Kind)
	}
}

func TestDecodeRequireBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/", http.NoBody)
	var v map[string]any
	err := Decode(r, &v, RequireBody())
	var de *DecodeError
	if !errors.As(err, &de) || de.Kind != DecodeKindEmpty {
		t.Errorf("err = %v, want DecodeKindEmpty", err)
	}
}

func TestDecodeNilBody(t *testing.T) {
	r := &http.Request{Body: nil}
	r = r.WithContext(context.Background())
	var v map[string]any
	if err := Decode(r, &v); err != nil {
		t.Errorf("Decode nil body should be no-op, got %v", err)
	}
}

func TestDecodeTrailingJSON(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"x":1}{"x":2}`))
	var v map[string]int
	err := Decode(r, &v)
	var de *DecodeError
	_ = errors.As(err, &de)
	if de.Kind != DecodeKindTrailing {
		t.Errorf("kind = %v, want Trailing", de.Kind)
	}
}

func TestWriteDecodeErrorTranslatesStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)

	err := &DecodeError{Kind: DecodeKindTooLarge, msg: "too big"}
	_ = WriteDecodeError(rec, r, err)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}

	var body ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Code != "request_too_large" {
		t.Errorf("code = %q", body.Error.Code)
	}
}

func TestWriteDecodeErrorEmptyBody(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	_ = WriteDecodeError(rec, r, &DecodeError{Kind: DecodeKindEmpty, msg: "no body"})
	if rec.Code != 400 {
		t.Errorf("status = %d", rec.Code)
	}
	var body ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Error.Code != "request_body_empty" {
		t.Errorf("code = %q", body.Error.Code)
	}
}

func TestWriteDecodeErrorFallsBackTo500(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	_ = WriteDecodeError(rec, r, errors.New("not a DecodeError"))
	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestDecodeKindString(t *testing.T) {
	for _, tc := range []struct {
		k    DecodeKind
		want string
	}{
		{DecodeKindMalformed, "malformed"},
		{DecodeKindUnknownField, "unknown_field"},
		{DecodeKindWrongType, "wrong_type"},
		{DecodeKindTooLarge, "too_large"},
		{DecodeKindEmpty, "empty"},
		{DecodeKindTrailing, "trailing"},
		{DecodeKindUnknown, "unknown"},
	} {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("%v.String() = %q, want %q", tc.k, got, tc.want)
		}
	}
}

// io.Reader that always errors — for catch-all behavior.
type erroringBody struct{}

func (erroringBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }
func (erroringBody) Close() error             { return nil }

func TestDecodeReadError(t *testing.T) {
	r := httptest.NewRequest("POST", "/", erroringBody{})
	r.Body = io.NopCloser(erroringBody{})
	var v map[string]any
	err := Decode(r, &v)
	if err == nil {
		t.Fatal("expected error from broken body")
	}
}
