package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetUsers(t *testing.T) {
	// Create a request to pass to the handler
	req, err := http.NewRequest("GET", "/users", nil)
	if err != nil {
		t.Fatalf("Could not create request: %v", err)
	}

	// Create a response recorder to record the response
	rr := httptest.NewRecorder()

	// Create a handler from the GetUsers function
	handler := http.HandlerFunc(GetUsers())

	// Serve the request
	handler.ServeHTTP(rr, req)

	// Check the status code
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v, want %v", status, http.StatusOK)
	}

	// Check the response body
	var users []User
	err = json.Unmarshal(rr.Body.Bytes(), &users)
	if err != nil {
		t.Fatalf("Response body did not marshal as a list of User: %v", err)
	}
}
