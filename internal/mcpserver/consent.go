package mcpserver

import (
	"fmt"
	"html"
	"net/http"
	"sync"
)

// ConsentStore records completion of out-of-band (URL-mode elicitation)
// flows. The connect_calendar tool consults it on retry so that "accept"
// from the client only counts once the user has actually visited the URL.
type ConsentStore interface {
	Complete(elicitationID string)
	Completed(elicitationID string) bool
}

// MemoryConsent is an in-memory ConsentStore.
type MemoryConsent struct {
	mu   sync.Mutex
	done map[string]bool
}

// NewMemoryConsent returns an empty MemoryConsent.
func NewMemoryConsent() *MemoryConsent { return &MemoryConsent{done: map[string]bool{}} }

func (m *MemoryConsent) Complete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.done[id] = true
}

func (m *MemoryConsent) Completed(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.done[id]
}

// ConsentHandler serves the stand-in "third-party consent page" that a
// URL-mode elicitation points at. Visiting /consent?id=X marks X complete.
// A real deployment would redirect to the provider's OAuth page and mark
// completion from the provider's callback instead.
func ConsentHandler(store ConsentStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" || len(id) > 128 {
			http.Error(w, "missing or invalid id", http.StatusBadRequest)
			return
		}
		store.Complete(id)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>Connected</title><p>Calendar connected (%s). You can close this tab.</p>", html.EscapeString(id))
	})
}
