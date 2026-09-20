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

func TestRuntimeRequiresEvidenceBackedDiagnosisBeforeMitigation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query" {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"result": []any{}}})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	m, err := newRuntime(runtimeConfig{Root: filepath.Join("..", ".."), TrafficURL: server.URL, PromURL: server.URL, LBURL: server.URL, API1URL: server.URL, API2URL: server.URL, DependencyURL: server.URL, HistoryPath: filepath.Join(t.TempDir(), "runs.json")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.start(context.Background(), "cpu-saturation"); err != nil {
		t.Fatal(err)
	}
	if err := m.setCapacity(context.Background(), 2); err == nil || !strings.Contains(err.Error(), "diagnosis") {
		t.Fatalf("setCapacity() error = %v, want diagnosis gate", err)
	}
	if err := m.setDiagnosis("dependency", "Dependency metrics are slower than API metrics."); err != nil {
		t.Fatal(err)
	}
	if err := m.setCapacity(context.Background(), 2); err == nil || !strings.Contains(err.Error(), "diagnosis") {
		t.Fatalf("wrong diagnosis was accepted: %v", err)
	}
	if err := m.setDiagnosis("api-capacity", "API CPU is saturated while dependency latency remains healthy."); err != nil {
		t.Fatal(err)
	}
	if err := m.setCapacity(context.Background(), 2); err != nil {
		t.Fatalf("correct diagnosis should unlock mitigation: %v", err)
	}
}

func TestSuccessfulMissionAwardsProgressionOnlyOnce(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/query" {
			value := "1000"
			if strings.Contains(r.URL.Query().Get("query"), "histogram_quantile") {
				value = "0.1"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"result": []any{map[string]any{"value": []any{"0", value}}}}})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	dir := t.TempDir()
	m, err := newRuntime(runtimeConfig{Root: filepath.Join("..", ".."), TrafficURL: server.URL, PromURL: server.URL, LBURL: server.URL, API1URL: server.URL, API2URL: server.URL, DependencyURL: server.URL, HistoryPath: filepath.Join(dir, "runs.json"), ProfilePath: filepath.Join(dir, "learner.json")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.start(context.Background(), "cpu-saturation"); err != nil {
		t.Fatal(err)
	}
	if err := m.setDiagnosis("api-capacity", "API CPU is saturated while dependency latency remains healthy."); err != nil {
		t.Fatal(err)
	}
	if err := m.setCapacity(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	first := m.check(context.Background())
	if first["passed"] != true || first["score"] != 100 || first["xpAwarded"] != 100 {
		t.Fatalf("first check = %#v", first)
	}
	second := m.check(context.Background())
	if second["xpAwarded"] != 0 || m.profile.Profile().XP != 100 {
		t.Fatalf("repeated check awarded XP: %#v, profile=%+v", second, m.profile.Profile())
	}
}
