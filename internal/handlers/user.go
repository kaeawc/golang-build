package handlers

import (
	"encoding/json"
	"net/http"
)

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func GetUsers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		users := []User{
			{ID: 1, Name: "Alice"},
			{ID: 2, Name: "Bob"},
			{ID: 3, Name: "Charlie"},
		}

		json.NewEncoder(w).Encode(users)
	}
}
