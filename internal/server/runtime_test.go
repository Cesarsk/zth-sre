package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeLifecycleResetsAfterTrafficFailure(t *testing.T) {
	failTraffic := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/start") && failTraffic {
			http.Error(w, "traffic unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/api/v1/query" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"result": []any{}}})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	m, err := newRuntime(runtimeConfig{
		Root: filepath.Join("..", ".."), TrafficURL: server.URL, PromURL: server.URL,
		LBURL: server.URL, API1URL: server.URL, API2URL: server.URL,
		DependencyURL: server.URL, HistoryPath: filepath.Join(t.TempDir(), "runs.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.start(context.Background(), "cpu-saturation"); err == nil {
		t.Fatal("expected traffic start failure")
	}
	if got := m.status(); got.(map[string]any)["state"] != "idle" {
		t.Fatalf("failed start left active state: %#v", got)
	}
	failTraffic = false
	if _, err := m.start(context.Background(), "cpu-saturation"); err != nil {
		t.Fatalf("restart after failed start: %v", err)
	}
	if _, err := m.start(context.Background(), "cpu-saturation"); err == nil {
		t.Fatal("expected concurrent run rejection")
	}
	if err := m.reset(context.Background()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if got := m.status(); got.(map[string]any)["state"] != "idle" {
		t.Fatalf("reset left active state: %#v", got)
	}
}
