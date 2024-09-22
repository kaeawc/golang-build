package middleware

import (
	"bytes"
	"net/http"
)

func ContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Create a response writer wrapper to inspect the response body
		rw := &responseWriter{ResponseWriter: w}

		if rw.Header().Get("Content-Type") == "" {
			contentType := http.DetectContentType(rw.body.Bytes())
			rw.Header().Set("Content-Type", contentType)
		}

		next.ServeHTTP(rw, r)
	})
}

type responseWriter struct {
	http.ResponseWriter
	body   bytes.Buffer // Store response body for sniffing content type
	status int
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.status = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}
