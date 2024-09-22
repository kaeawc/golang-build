package main

import (
	"log"
	"net/http"

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
	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", router))
}
