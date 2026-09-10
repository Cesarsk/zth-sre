package apilb

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealthAndRoundRobin(t *testing.T) {
	var first, second atomic.Int32
	backend := func(name string, count *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				w.WriteHeader(http.StatusOK)
				return
			}
			count.Add(1)
			if got := r.Header.Get("X-SRE-Run-ID"); got != "run-1" {
				t.Errorf("%s got run ID %q", name, got)
			}
			fmt.Fprintln(w, name)
		}))
	}
	one, two := backend("one", &first), backend("two", &second)
	defer one.Close()
	defer two.Close()
	handler, err := New(Config{Backend1URL: one.URL, Backend2URL: two.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()

	server := httptest.NewServer(handler)
	defer server.Close()
	response, err := http.Get(server.URL + "/healthz")
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("healthz: response=%v err=%v", response, err)
	}
	response.Body.Close()

	request, _ := http.NewRequest(http.MethodPut, server.URL+"/admin/state", strings.NewReader(`{"active_backends":2}`))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("activate second backend status = %d", response.StatusCode)
	}

	for range 4 {
		request, _ := http.NewRequest(http.MethodGet, server.URL+"/", nil)
		request.Header.Set("X-SRE-Run-ID", "run-1")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("proxy status = %d", response.StatusCode)
		}
	}
	if first.Load() != 2 || second.Load() != 2 {
		t.Fatalf("round robin counts = %d, %d", first.Load(), second.Load())
	}
}

func TestStateValidationAndNoSSRF(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprintln(w, "fixed")
	}))
	defer backend.Close()
	handler, err := New(Config{Backend1URL: backend.URL, Backend2URL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	server := httptest.NewServer(handler)
	defer server.Close()

	for _, body := range []string{
		`{}`,
		`{"active_backends":0}`,
		`{"active_backends":3}`,
		`{"active_backends":1,"backend_url":"http://127.0.0.1:1"}`,
		`{"active_backends":1} trailing`,
	} {
		request, _ := http.NewRequest(http.MethodPut, server.URL+"/admin/state", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q status = %d", body, response.StatusCode)
		}
	}

	request, _ := http.NewRequest(http.MethodPut, server.URL+"/admin/state", strings.NewReader(`{"active_backends":2}`))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("valid state status = %d", response.StatusCode)
	}
	if calls.Load() != 0 {
		t.Fatalf("admin state contacted backend %d times", calls.Load())
	}
}

func TestProxyErrorsAre503(t *testing.T) {
	redirect := httptest.NewServer(http.RedirectHandler("http://example.invalid", http.StatusFound))
	defer redirect.Close()
	handler, err := New(Config{Backend1URL: redirect.URL, Backend2URL: redirect.URL, Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	server := httptest.NewServer(handler)
	defer server.Close()
	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("redirect status = %d", response.StatusCode)
	}
}
