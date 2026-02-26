package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/kaeawc/golang-build/internal/cache"
	"github.com/kaeawc/golang-build/internal/db"
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

		cached, err := c.Get(r.Context(), cacheKey)
		if err == nil {
			if _, err := w.Write([]byte(cached)); err != nil {
				log.Printf("write error: %v", err)
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
			log.Printf("cache set error: %v", err)
		}

		if _, err := w.Write(data); err != nil {
			log.Printf("write error: %v", err)
		}
	}
}
