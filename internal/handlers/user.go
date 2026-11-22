package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/kaeawc/golang-build/internal/cache"
	"github.com/kaeawc/golang-build/internal/db"
	"github.com/kaeawc/golang-build/internal/logger"
)

type User struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type UserQuerier interface {
	GetUsers(ctx context.Context) ([]db.User, error)
}

func GetUsers(querier UserQuerier, c cache.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const cacheKey = "users"
		w.Header().Set("Content-Type", "application/json")

		cached, err := c.Get(r.Context(), cacheKey)
		if err == nil {
			if _, err := w.Write([]byte(cached)); err != nil { // #nosec G705 -- value is our own marshaled JSON, not user input
				logger.FromContext(r.Context()).WarnContext(r.Context(), "write response", "err", err)
			}
			return
		}

		dbUsers, err := querier.GetUsers(r.Context())
		if err != nil {
			http.Error(w, "Failed to get users", http.StatusInternalServerError)
			return
		}

		users := make([]User, len(dbUsers))
		for i, u := range dbUsers {
			users[i] = User{ID: u.ID, Name: u.Name}
		}

		data, err := json.Marshal(users)
		if err != nil {
			http.Error(w, "Failed to encode users", http.StatusInternalServerError)
			return
		}

		if err := c.Set(r.Context(), cacheKey, string(data), 30*time.Second); err != nil {
			logger.FromContext(r.Context()).WarnContext(r.Context(), "cache set", "key", cacheKey, "err", err)
		}

		if _, err := w.Write(data); err != nil {
			logger.FromContext(r.Context()).WarnContext(r.Context(), "write response", "err", err)
		}
	}
}
