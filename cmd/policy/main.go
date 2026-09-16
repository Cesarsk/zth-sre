package main

import (
	"log"
	"net/http"
	"os"

	"sre-lab/internal/policy"
)

func main() {
	service, err := policy.New(env("UPSTREAM", "http://dependency:8080"))
	if err != nil {
		log.Fatal(err)
	}
	log.Print("policy gateway listening on :8080")
	if err := http.ListenAndServe(":8080", service); err != nil {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
