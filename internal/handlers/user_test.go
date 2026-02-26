package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kaeawc/golang-build/internal/db"
)

type mockQuerier struct{}

func (m *mockQuerier) GetUsers(_ context.Context) ([]db.User, error) {
	return []db.User{
		{ID: 1, Name: "Alice"},
		{ID: 2, Name: "Bob"},
		{ID: 3, Name: "Charlie"},
	}, nil
}

type mockCache struct{}

func (m *mockCache) Get(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("miss")
}

func (m *mockCache) Set(_ context.Context, _ string, _ string, _ time.Duration) error {
	return nil
}

func (m *mockCache) Close() {}

func TestGetUsers(t *testing.T) {
	req, err := http.NewRequest("GET", "/users", nil)
	if err != nil {
		t.Fatalf("Could not create request: %v", err)
	}

	rr := httptest.NewRecorder()

	handler := http.HandlerFunc(GetUsers(&mockQuerier{}, &mockCache{}))
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v, want %v", status, http.StatusOK)
	}

	var users []User
	err = json.Unmarshal(rr.Body.Bytes(), &users)
	if err != nil {
		t.Fatalf("Response body did not marshal as a list of User: %v", err)
	}
}
