package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"

	"github.com/kaeawc/golang-build/internal/db"
)

// PgxStore is the live UserStore, backed by the sqlc-generated queries in
// internal/db. Pass a *pgxpool.Pool (or a pgx.Tx in tests) as the DBTX.
type PgxStore struct {
	q *db.Queries
}

// NewPgxStore returns a PgxStore that issues queries through conn.
func NewPgxStore(conn db.DBTX) *PgxStore {
	return &PgxStore{q: db.New(conn)}
}

func (s *PgxStore) List(ctx context.Context, afterID int64, limit int) ([]User, error) {
	if limit <= 0 || limit > math.MaxInt32 {
		return nil, fmt.Errorf("list users: invalid limit %d", limit)
	}
	rows, err := s.q.ListUsersAfter(ctx, db.ListUsersAfterParams{ID: afterID, Limit: int32(limit)})
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	out := make([]User, len(rows))
	for i, r := range rows {
		out[i] = User(r)
	}
	return out, nil
}

func (s *PgxStore) Get(ctx context.Context, id int64) (User, error) {
	u, err := s.q.GetUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, fmt.Errorf("%w: id %d", ErrNotFound, id)
	}
	if err != nil {
		return User{}, fmt.Errorf("get user %d: %w", id, err)
	}
	return User(u), nil
}

func (s *PgxStore) Create(ctx context.Context, name string) (User, error) {
	name, err := ValidateName(name)
	if err != nil {
		return User{}, err
	}
	u, err := s.q.CreateUser(ctx, name)
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return User(u), nil
}

func (s *PgxStore) Delete(ctx context.Context, id int64) error {
	n, err := s.q.DeleteUser(ctx, id)
	if err != nil {
		return fmt.Errorf("delete user %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id %d", ErrNotFound, id)
	}
	return nil
}
