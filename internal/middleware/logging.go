package middleware

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// Logging logs the request details
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		uri := strings.NewReplacer("\n", "", "\r", "").Replace(r.RequestURI)
		log.Printf("%s %s %s %s", r.Method, uri, r.RemoteAddr, time.Since(start)) // #nosec G706 -- newlines stripped above
	})
}
