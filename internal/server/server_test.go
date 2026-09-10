package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testConfig(t *testing.T, upstream string) Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>SRE Lab</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	return Config{Origins: []string{"http://localhost:8080"}, StaticDir: dir, APIURL: upstream, DependencyURL: upstream, ToolboxURL: upstream, PrometheusURL: upstream}
}

func request(h http.Handler, method, path, host, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Host = host
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("X-Forwarded-Host", "localhost:8080")
	r.Header.Set("X-Forwarded-Proto", "http")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestBoundariesAndStatic(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, r.URL.RequestURI()) }))
	defer up.Close()
	c := testConfig(t, up.URL)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("secret"), 0600)
	if err := os.Symlink(outside, filepath.Join(c.StaticDir, "secret.txt")); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(c.StaticDir, "directory"), 0700)
	h, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	for _, tc := range []struct {
		method, path, host, origin string
		code                       int
	}{
		{"GET", "/healthz", "localhost:8080", "", 200},
		{"GET", "/healthz", "evil.example", "", 403},
		{"GET", "/", "localhost:8080.evil.example", "", 403},
		{"GET", "/api/status", "localhost", "", 403},
		{"POST", "/healthz", "localhost:8080", "", 405},
		{"GET", "/terminal", "localhost:8080", "", 403},
		{"GET", "/terminal", "localhost:8080", "null", 403},
		{"GET", "/terminal", "localhost:8080", "http://localhost:8080.evil", 403},
		{"GET", "/terminal", "localhost:8080", "https://localhost:8080", 403},
		{"GET", "/terminal", "localhost:8080", "http://localhost:8080/", 403},
		{"GET", "/terminal", "localhost:8080", "http://localhost:8080", 400},
		{"GET", "/api/unknown", "localhost:8080", "", 404},
		{"GET", "/", "localhost:8080", "", 200},
		{"GET", "/learning/overview", "localhost:8080", "", 200},
		{"GET", "/missing.js", "localhost:8080", "", 404},
		{"GET", "/secret.txt", "localhost:8080", "", 404},
		{"GET", "/directory/", "localhost:8080", "", 404},
		{"GET", "/%2e%2e/secret.txt", "localhost:8080", "", 404},
		{"GET", "/prometheus/api/v1/admin/tsdb/delete_series", "localhost:8080", "", 404},
		{"GET", "/prometheus/-/quit", "localhost:8080", "", 404},
		{"GET", "/prometheus/-/reload", "localhost:8080", "", 404},
		{"GET", "/prometheus/api/v1/status/config", "localhost:8080", "", 404},
		{"POST", "/prometheus/api/v1/query", "localhost:8080", "", 405},
		{"HEAD", "/prometheus/api/v1/query", "localhost:8080", "", 405},
		{"GET", "/prometheus/static/../-/reload", "localhost:8080", "", 404},
	} {
		t.Run(tc.method+tc.path+tc.host+tc.origin, func(t *testing.T) {
			w := request(h, tc.method, tc.path, tc.host, tc.origin)
			if w.Code != tc.code {
				t.Fatalf("got %d: %s; want %d", w.Code, w.Body.String(), tc.code)
			}
			if !strings.Contains(w.Header().Get("Content-Security-Policy"), "style-src 'self' 'unsafe-inline'") {
				t.Fatal("missing xterm CSP")
			}
			inlineScripts := strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self' 'unsafe-inline'")
			if inlineScripts != strings.HasPrefix(tc.path, "/prometheus/") {
				t.Fatal("inline script exception must be limited to Prometheus")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("rejected request reached upstream: %d", calls.Load())
	}
}

func TestProgressPersistsAndRejectsCrossOriginWrites(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer up.Close()
	c := testConfig(t, up.URL)
	c.ProgressPath = filepath.Join(t.TempDir(), "progress.json")
	h, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	put := func(origin string, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/progress/first-investigation", bytes.NewBufferString(body))
		r.Host = "localhost:8080"
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := put("http://evil.example", `{"completedSteps":[1],"notes":"x"}`); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", w.Code)
	}
	if w := put("http://localhost:8080", `{"completedSteps":[3,1,3],"notes":"Evidence recorded"}`); w.Code != http.StatusOK {
		t.Fatalf("write status = %d: %s", w.Code, w.Body.String())
	}
	w := request(h, http.MethodGet, "/api/progress/first-investigation", "localhost:8080", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"completedSteps":[1,3]`) || !strings.Contains(w.Body.String(), `"notes":"Evidence recorded"`) {
		t.Fatalf("read response = %d: %s", w.Code, w.Body.String())
	}
	if w := put("http://localhost:8080", `{"unknown":true}`); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid write status = %d", w.Code)
	}
}

func TestPrometheusFixedProxy(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("Authorization") != "" {
			t.Error("client credentials/forwarding reached upstream")
		}
		io.WriteString(w, r.URL.RequestURI())
	}))
	defer up.Close()
	h, err := New(testConfig(t, up.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	for _, p := range []string{"/", "/query", "/assets/app.js", "/static/app.js", "/api/v1/query?query=up&url=http://evil.example", "/api/v1/label/job/values"} {
		r := httptest.NewRequest("GET", "/prometheus"+p, nil)
		r.Host = "localhost:8080"
		r.Header.Set("X-Forwarded-For", "evil")
		r.Header.Set("Forwarded", "host=evil")
		r.Header.Set("Authorization", "secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != p {
			t.Fatalf("proxy %s: %d %s", p, w.Code, w.Body.String())
		}
	}
}

func TestLiveStatusParallelAndDegraded(t *testing.T) {
	var entered atomic.Int32
	var fail atomic.Bool
	barrier := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" && r.URL.Path != "/-/ready" {
			t.Error("wrong health endpoint")
		}
		if entered.Add(1) == 4 {
			close(barrier)
		}
		select {
		case <-barrier:
		case <-r.Context().Done():
			return
		}
		if fail.Load() && r.URL.Path == "/-/ready" {
			w.WriteHeader(503)
		}
	}))
	defer up.Close()
	h, err := New(testConfig(t, up.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	for _, healthy := range []bool{true, false} {
		fail.Store(!healthy)
		w := request(h, "GET", "/api/status", "localhost:8080", "")
		var result struct {
			Status     string
			Phase      int
			Components []struct{ Name, Status string }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		want := "healthy"
		if !healthy {
			want = "degraded"
		}
		if w.Code != 200 || result.Status != want || result.Phase != 1 || len(result.Components) != 4 {
			t.Fatalf("bad status: %s", w.Body.String())
		}
		for i, name := range []string{"api", "dependency", "toolbox", "prometheus"} {
			status := "healthy"
			if !healthy && name == "prometheus" {
				status = "unavailable"
			}
			if result.Components[i].Name != name || result.Components[i].Status != status {
				t.Fatalf("bad component: %+v", result.Components[i])
			}
		}
	}
}

func TestStatusTimeoutAndLiveness(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer up.Close()
	h, err := New(testConfig(t, up.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	start := time.Now()
	w := request(h, "GET", "/api/status", "localhost:8080", "")
	if time.Since(start) > 3*time.Second || w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"degraded"`) {
		t.Fatalf("unbounded/bad status: %s", w.Body.String())
	}
	if w := request(h, "GET", "/healthz", "localhost:8080", ""); w.Code != 200 {
		t.Fatal("liveness depends on upstream")
	}
}

func TestTerminalProxyAndShutdown(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/terminal" || r.Header.Get("Origin") != "http://localhost:8080" {
			t.Error("wrong terminal upstream contract")
		}
		u := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			kind, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(kind, data); err != nil {
				return
			}
		}
	}))
	defer up.Close()
	h, err := New(testConfig(t, up.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	s := httptest.NewServer(h)
	defer s.Close()
	headers := http.Header{"Host": []string{"localhost:8080"}, "Origin": []string{"http://localhost:8080"}}
	c, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.URL, "http")+"/terminal?target=evil", headers)
	if err != nil {
		t.Fatalf("upgrade: %v (%v)", err, resp)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := c.WriteMessage(websocket.BinaryMessage, []byte("PTY output")); err != nil {
		t.Fatal(err)
	}
	kind, data, err := c.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || string(data) != "PTY output" {
		t.Fatalf("proxy frame %d %q %v", kind, data, err)
	}
	h.Close()
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("shutdown left WebSocket open")
	}
}

func TestInvalidOriginsAndUpstreams(t *testing.T) {
	for _, s := range []string{"*", "null", "http://localhost:8080/", "http://user@localhost:8080", "https://*.example", "http://localhost:8080?", "http://localhost:8080#fragment"} {
		if _, _, err := ParseOrigins([]string{s}); err == nil {
			t.Errorf("accepted origin %q", s)
		}
	}
	for _, s := range []string{"file:///etc/passwd", "http://user@api:8080", "http://api:8080/path", "http://api:8080?url=evil"} {
		if _, err := upstream(s); err == nil {
			t.Errorf("accepted upstream %q", s)
		}
	}
}

func TestProxyRedirectBoundary(t *testing.T) {
	for _, tc := range []struct {
		location string
		code     int
		want     string
	}{
		{"/query", 302, "/prometheus/query"},
		{"/prometheus/query", 302, "/prometheus/query"},
		{"https://evil.example/query", 502, ""},
		{"//evil.example/query", 502, ""},
		{"/-/reload", 502, ""},
		{"/static/../-/reload", 502, ""},
	} {
		t.Run(tc.location, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", tc.location)
				w.WriteHeader(302)
			}))
			defer up.Close()
			h, err := New(testConfig(t, up.URL))
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			w := request(h, "GET", "/prometheus/", "localhost:8080", "")
			if w.Code != tc.code || w.Header().Get("Location") != tc.want {
				t.Fatalf("got %d %q", w.Code, w.Header().Get("Location"))
			}
		})
	}
}
