package mcpserver

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// User is the domain object exposed through tools, resources, and prompts.
// The jsonschema tags double as tool output-schema descriptions.
type User struct {
	ID   int64  `json:"id" jsonschema:"stable numeric user identifier"`
	Name string `json:"name" jsonschema:"display name"`
}

// ErrNotFound is returned by a UserStore when the requested user does not
// exist.
var ErrNotFound = errors.New("mcpserver: user not found")

// ErrInvalidName is returned by UserStore.Create for empty or oversized names.
var ErrInvalidName = errors.New("mcpserver: invalid user name")

// MaxNameLength bounds user names accepted by Create.
const MaxNameLength = 100

// UserStore is the persistence boundary for the MCP server. MemoryStore is
// the in-memory fake (zero infrastructure); PgxStore is the live
// implementation backed by internal/db.
type UserStore interface {
	// List returns up to limit users with ID > afterID, ordered by ID.
	List(ctx context.Context, afterID int64, limit int) ([]User, error)
	Get(ctx context.Context, id int64) (User, error)
	Create(ctx context.Context, name string) (User, error)
	// Delete removes the user, returning ErrNotFound if it did not exist.
	Delete(ctx context.Context, id int64) error
}

// ValidateName normalises and validates a user name.
func ValidateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > MaxNameLength {
		return "", fmt.Errorf("%w: must be 1-%d bytes", ErrInvalidName, MaxNameLength)
	}
	return name, nil
}

// MemoryStore is a concurrency-safe in-memory UserStore.
type MemoryStore struct {
	mu     sync.Mutex
	nextID int64
	users  map[int64]User
}

// NewMemoryStore returns a MemoryStore pre-populated with the given names.
func NewMemoryStore(names ...string) *MemoryStore {
	s := &MemoryStore{nextID: 1, users: make(map[int64]User)}
	for _, n := range names {
		if _, err := s.Create(context.Background(), n); err != nil {
			panic(fmt.Sprintf("NewMemoryStore: %v", err))
		}
	}
	return s
}

// DemoUsers is the default seed data for the in-memory backend.
var DemoUsers = []string{
	"Ada Lovelace", "Alan Turing", "Barbara Liskov", "Donald Knuth",
	"Edsger Dijkstra", "Frances Allen", "Grace Hopper", "Ken Thompson",
	"Margaret Hamilton", "Radia Perlman", "Rob Pike", "Robert Griesemer",
}

func (s *MemoryStore) List(ctx context.Context, afterID int64, limit int) ([]User, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]User, 0, min(limit, len(s.users)))
	for _, u := range s.users {
		if u.ID > afterID {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b User) int { return cmp.Compare(a.ID, b.ID) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) Get(ctx context.Context, id int64) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return User{}, fmt.Errorf("%w: id %d", ErrNotFound, id)
	}
	return u, nil
}

func (s *MemoryStore) Create(ctx context.Context, name string) (User, error) {
	if err := ctx.Err(); err != nil {
		return User{}, err
	}
	name, err := ValidateName(name)
	if err != nil {
		return User{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := User{ID: s.nextID, Name: name}
	s.users[u.ID] = u
	s.nextID++
	return u, nil
}

func (s *MemoryStore) Delete(ctx context.Context, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[id]; !ok {
		return fmt.Errorf("%w: id %d", ErrNotFound, id)
	}
	delete(s.users, id)
	return nil
}
