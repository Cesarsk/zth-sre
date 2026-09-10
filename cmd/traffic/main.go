package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sre-lab/traffic"
)

func main() {
	controller := traffic.New(traffic.ConfigFromEnv())
	server := &http.Server{
		Addr:              env("LISTEN_ADDR", ":8080"),
		Handler:           controller,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	signals, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	shutdownDone := make(chan struct{})
	go func() {
		<-signals.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		defer close(shutdownDone)
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("HTTP shutdown: %v", err)
		}
		if err := controller.Shutdown(ctx); err != nil {
			log.Printf("k6 shutdown: %v", err)
		}
	}()

	err := server.ListenAndServe()
	if !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-shutdownDone
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
