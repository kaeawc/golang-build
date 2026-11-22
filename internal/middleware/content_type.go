package middleware

import (
	"net/http"
)

// ContentType sets Content-Type from the first bytes the handler writes,
// when the handler has not set one. Place it inside compressing
// middleware (after Gzip in an httpmw.Compose list) so it sniffs the
// plain body rather than compressed bytes.
func ContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&sniffWriter{ResponseWriter: w}, r)
	})
}

type sniffWriter struct {
	http.ResponseWriter
	sniffed bool
}

func (s *sniffWriter) WriteHeader(statusCode int) {
	s.sniffed = true
	s.ResponseWriter.WriteHeader(statusCode)
}

func (s *sniffWriter) Write(b []byte) (int, error) {
	if !s.sniffed {
		s.sniffed = true
		if s.Header().Get("Content-Type") == "" {
			s.Header().Set("Content-Type", http.DetectContentType(b))
		}
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *sniffWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
