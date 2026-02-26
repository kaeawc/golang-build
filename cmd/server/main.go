package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kaeawc/golang-build/internal/cache"
	"github.com/kaeawc/golang-build/internal/db"
	"github.com/kaeawc/golang-build/internal/handlers"
	"github.com/kaeawc/golang-build/internal/middleware"
)

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	valkeyAddr := os.Getenv("VALKEY_ADDR")
	if valkeyAddr == "" {
		log.Fatal("VALKEY_ADDR environment variable is required")
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	c, err := cache.New(valkeyAddr)
	if err != nil {
		log.Fatalf("Failed to connect to cache: %v", err)
	}
	defer c.Close()

	querier := db.New(pool)

	router := mux.NewRouter()
	router.Use(middleware.Logging)
	router.Use(middleware.Recover)
	router.Use(middleware.ContentType)
	router.HandleFunc("/users", handlers.GetUsers(querier, c)).Methods("GET")
	router.HandleFunc("/ws", handlers.WebSocket())

	server := &http.Server{
		Addr:         ":8080",
		Handler:      router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Println("Server starting on :8080")
	log.Fatal(server.ListenAndServe())
}
