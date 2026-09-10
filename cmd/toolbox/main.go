package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"sre-lab/internal/server"
	"sre-lab/internal/terminal"
)

func main() {
	agent, err := terminal.New(server.OriginsFromEnv())
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Addr: ":8080", Handler: agent, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		agent.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			log.Print(err)
			srv.Close()
		}
		close(done)
	}()
	log.Print("toolbox listening on :8080")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	<-done
}
