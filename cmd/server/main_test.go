package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/kaeawc/golang-build/internal/handlers"
)

// Mock middleware for testing purposes
func mockLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock behavior for logging
		next.ServeHTTP(w, r)
	})
}

func mockRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock behavior for recover
		next.ServeHTTP(w, r)
	})
}

func mockContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock behavior for content type
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

// Create a test version of the main app router
func newTestRouter() *mux.Router {
	router := mux.NewRouter()
	router.Use(mockLogging)
	router.Use(mockRecover)
	router.Use(mockContentType)
	router.HandleFunc("/users", handlers.GetUsers()).Methods("GET")
	return router
}

func TestMainAppStartup(t *testing.T) {
	// Initialize the test router
	router := newTestRouter()

	// Create a test request to the /users endpoint
	req, err := http.NewRequest("GET", "/users", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	// Create a response recorder to capture the response
	rr := httptest.NewRecorder()

	// Serve the test request
	router.ServeHTTP(rr, req)

	// Check the status code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Expected status code 200, but got %v", status)
	}

	// Check the Content-Type header set by mock middleware
	if contentType := rr.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Expected Content-Type application/json, but got %v", contentType)
	}

	// Check if the response body is valid JSON
	var users []handlers.User
	err = json.Unmarshal(rr.Body.Bytes(), &users)
	if err != nil {
		t.Errorf("Response body did not contain valid JSON: %v", err)
	}
}
