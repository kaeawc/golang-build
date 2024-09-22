package main

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/kaeawc/golang-build/internal/handlers"
	"github.com/kaeawc/golang-build/internal/middleware"
)

func main() {

	router := mux.NewRouter()
	router.Use(middleware.Logging)
	router.Use(middleware.Recover)
	router.Use(middleware.ContentType)
	router.HandleFunc("/users", handlers.GetUsers()).Methods("GET")

	// Define the server with timeouts
	server := &http.Server{
		Addr:         ":8080",
		Handler:      router,
		ReadTimeout:  5 * time.Second,   // Timeout for reading the request
		WriteTimeout: 10 * time.Second,  // Timeout for writing the response
		IdleTimeout:  120 * time.Second, // Timeout for idle connections
	}

	log.Println("Server starting on :8080")
	// Start the server with proper timeout settings
	log.Fatal(server.ListenAndServe())
}
