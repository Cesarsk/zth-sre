package traffic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestControllerValidationLifecycleAndReaping(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pid")
	envPath := filepath.Join(dir, "env")
	k6Path := filepath.Join(dir, "fake-k6")
	fake := "#!/bin/sh\nprintf '%s' \"$$\" > \"$K6_PID_FILE\"\nprintf '%s|%s|%s|%s' \"$RUN_ID\" \"$SCENARIO\" \"$PROFILE\" \"$TARGET_URL\" > \"$K6_ENV_FILE\"\ntrap 'exit 0' TERM INT\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(k6Path, []byte(fake), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("K6_PID_FILE", pidPath)
	t.Setenv("K6_ENV_FILE", envPath)
	controller := New(Config{K6Path: k6Path, TargetURL: "http://api-lb:8080", PrometheusRWURL: "http://prometheus:9090/api/v1/write"})

	for _, tc := range []struct {
		method, path, body string
		contentType        string
		want               int
	}{
		{http.MethodPost, "/healthz", "", "", http.StatusMethodNotAllowed},
		{http.MethodPost, "/status", "", "", http.StatusMethodNotAllowed},
		{http.MethodGet, "/start", "", "", http.StatusMethodNotAllowed},
		{http.MethodGet, "/stop", "", "", http.StatusMethodNotAllowed},
		{http.MethodPost, "/start", `{"runID":"run-1","scenario":"cpu-saturation","profile":"cpu","extra":true}`, "application/json", http.StatusBadRequest},
		{http.MethodPost, "/start", `{"runID":"run-1","scenario":"alerting","profile":"cpu"}`, "application/json", http.StatusBadRequest},
		{http.MethodPost, "/start", `{"runID":"run-1","scenario":"cpu-saturation","profile":"cpu"}`, "text/plain", http.StatusUnsupportedMediaType},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.contentType != "" {
				request.Header.Set("Content-Type", tc.contentType)
			}
			response := httptest.NewRecorder()
			controller.ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.want, response.Body.String())
			}
		})
	}

	start := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/start", bytes.NewBufferString(`{"runID":"run-1","scenario":"cpu-saturation","profile":"cpu"}`))
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
		response := httptest.NewRecorder()
		controller.ServeHTTP(response, request)
		return response
	}
	if response := start(); response.Code != http.StatusCreated {
		t.Fatalf("start status = %d: %s", response.Code, response.Body.String())
	}
	if response := start(); response.Code != http.StatusConflict {
		t.Fatalf("second start status = %d: %s", response.Code, response.Body.String())
	}

	pid := waitForPID(t, pidPath)
	got := waitForContent(t, envPath, "run-1|cpu-saturation|cpu|http://api-lb:8080")
	if string(got) != "run-1|cpu-saturation|cpu|http://api-lb:8080" {
		t.Fatalf("k6 environment = %q", got)
	}
	status := httptest.NewRecorder()
	controller.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/status", nil))
	var running statusResponse
	if err := json.Unmarshal(status.Body.Bytes(), &running); err != nil {
		t.Fatal(err)
	}
	if status.Code != http.StatusOK || running.Status != "running" || running.PID != pid || running.RunID != "run-1" {
		t.Fatalf("running status = %d %+v", status.Code, running)
	}

	stop := httptest.NewRecorder()
	controller.ServeHTTP(stop, httptest.NewRequest(http.MethodPost, "/stop", nil))
	if stop.Code != http.StatusOK || !strings.Contains(stop.Body.String(), `"status":"idle"`) {
		t.Fatalf("stop status = %d: %s", stop.Code, stop.Body.String())
	}
	waitForReap(t, pid)
	if got := controller.status(); got.Status != "idle" {
		t.Fatalf("status after stop = %+v", got)
	}
}

func waitForContent(t *testing.T, path, want string) []byte {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && string(data) == want {
			return data
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	return data
}

func TestShutdownStopsActiveRun(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pid")
	k6Path := filepath.Join(dir, "fake-k6")
	if err := os.WriteFile(k6Path, []byte("#!/bin/sh\nprintf '%s' \"$$\" > \"$K6_PID_FILE\"\ntrap 'exit 0' TERM INT\nwhile :; do sleep 1; done\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("K6_PID_FILE", pidPath)
	controller := New(Config{K6Path: k6Path})
	if _, err := controller.start(startRequest{RunID: "run-2", Scenario: "alerting", Profile: "alert"}); err != nil {
		t.Fatal(err)
	}
	pid := waitForPID(t, pidPath)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := controller.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	waitForReap(t, pid)
	if _, err := controller.start(startRequest{RunID: "run-3", Scenario: "alerting", Profile: "alert"}); !errors.Is(err, errClosed) {
		t.Fatalf("start after shutdown error = %v", err)
	}
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(string(data))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fake k6 did not write its pid")
	return 0
}

func waitForReap(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("check process %d: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("fake k6 process %d was not reaped", pid)
}
