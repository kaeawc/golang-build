package mcpserver

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"sync"
	"time"

	"github.com/kaeawc/golang-build/internal/clock"
)

// ConsentStore tracks out-of-band (URL-mode elicitation) flows from issue to
// use. connect_calendar calls Begin when it hands out a consent URL, the
// consent page calls Complete, and the tool's retry calls Consume. Only
// issued, unexpired IDs can be completed, and a completed ID can be
// consumed once.
type ConsentStore interface {
	// Begin registers an ID the server has just issued.
	Begin(id string) error
	// Complete marks an issued, unexpired ID as done. It reports false (and
	// stores nothing) for unknown or expired IDs.
	Complete(id string) bool
	// Consume reports whether id was completed and not expired, removing
	// it so it cannot be used again. Pending IDs are left in place.
	Consume(id string) bool
}

// ErrConsentFull is returned by MemoryConsent.Begin when too many flows are
// pending at once.
var ErrConsentFull = errors.New("mcpserver: too many pending consent flows")

// maxPendingConsents bounds MemoryConsent memory even if a client keeps
// starting flows it never finishes. Entries also expire after
// requestStateTTL, the same window in which the signed requestState that
// carries the ID is accepted.
const maxPendingConsents = 10_000

type consentEntry struct {
	expires time.Time
	done    bool
}

// MemoryConsent is an in-memory ConsentStore with expiry driven by an
// injected clock. Expired entries are evicted lazily: an entry when it is
// looked up, and all of them when Begin hits the size cap.
type MemoryConsent struct {
	clk clock.Clock
	ttl time.Duration

	mu      sync.Mutex
	entries map[string]consentEntry
}

// NewMemoryConsent returns an empty MemoryConsent. A nil clk means the
// system clock.
func NewMemoryConsent(clk clock.Clock) *MemoryConsent {
	if clk == nil {
		clk = clock.Default
	}
	return &MemoryConsent{clk: clk, ttl: requestStateTTL, entries: map[string]consentEntry{}}
}

func (m *MemoryConsent) Begin(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clk.Now()
	if len(m.entries) >= maxPendingConsents {
		m.evictLocked(now) // only pay for a full sweep when at the cap
	}
	if len(m.entries) >= maxPendingConsents {
		return ErrConsentFull
	}
	m.entries[id] = consentEntry{expires: now.Add(m.ttl)}
	return nil
}

func (m *MemoryConsent) Complete(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.liveLocked(id)
	if !ok {
		return false
	}
	e.done = true
	m.entries[id] = e
	return true
}

func (m *MemoryConsent) Consume(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.liveLocked(id)
	if !ok || !e.done {
		return false
	}
	delete(m.entries, id)
	return true
}

// Len reports the number of live (unexpired) entries.
func (m *MemoryConsent) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evictLocked(m.clk.Now())
	return len(m.entries)
}

// liveLocked returns id's entry, evicting it instead if it has expired.
func (m *MemoryConsent) liveLocked(id string) (consentEntry, bool) {
	e, ok := m.entries[id]
	if ok && !m.clk.Now().Before(e.expires) {
		delete(m.entries, id)
		return consentEntry{}, false
	}
	return e, ok
}

// evictLocked drops every expired entry. Begin runs it only at the cap, so
// operations stay O(1), memory stays bounded by maxPendingConsents, and
// expired flows never block new ones.
func (m *MemoryConsent) evictLocked(now time.Time) {
	for id, e := range m.entries {
		if !now.Before(e.expires) {
			delete(m.entries, id)
		}
	}
}

// ConsentHandler serves the stand-in "third-party consent page" that a
// URL-mode elicitation points at. Visiting /consent?id=X completes X if the
// server issued it and it has not expired; anything else is a 404 and
// stores nothing. A real deployment would redirect to the provider's OAuth
// page and complete the flow from the provider's callback instead.
func ConsentHandler(store ConsentStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" || len(id) > 128 {
			http.Error(w, "missing or invalid id", http.StatusBadRequest)
			return
		}
		if !store.Complete(id) {
			http.Error(w, "unknown or expired consent request", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!doctype html><title>Connected</title><p>Calendar connected (%s). You can close this tab.</p>", html.EscapeString(id))
	})
}
