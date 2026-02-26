package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/kaeawc/golang-build/internal/db"
	"github.com/kaeawc/golang-build/internal/handlers"
)

type testQuerier struct{}

func (q *testQuerier) GetUsers(_ context.Context) ([]db.User, error) {
	return []db.User{
		{ID: 1, Name: "Alice"},
		{ID: 2, Name: "Bob"},
		{ID: 3, Name: "Charlie"},
	}, nil
}

type testCache struct{}

func (c *testCache) Get(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("miss")
}

func (c *testCache) Set(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}

func (c *testCache) Close() {}

func mockLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func mockRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func mockContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

func newTestRouter() *mux.Router {
	router := mux.NewRouter()
	router.Use(mockLogging)
	router.Use(mockRecover)
	router.Use(mockContentType)
	router.HandleFunc("/users", handlers.GetUsers(&testQuerier{}, &testCache{})).Methods("GET")
	return router
}

func TestMainAppStartup(t *testing.T) {
	router := newTestRouter()

	req, err := http.NewRequest("GET", "/users", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Expected status code 200, but got %v", status)
	}

	if contentType := rr.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, but got %v", contentType)
	}

	var users []handlers.User
	err = json.Unmarshal(rr.Body.Bytes(), &users)
	if err != nil {
		t.Errorf("Response body did not contain valid JSON: %v", err)
	}
}
