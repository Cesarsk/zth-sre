package demo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func startService(t *testing.T, role, upstream string) *httptest.Server {
	t.Helper()
	s, err := New(role, upstream)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	return server
}

func request(t *testing.T, method, endpoint string, wantCode int) string {
	t.Helper()
	req, err := http.NewRequest(method, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != wantCode {
		t.Fatalf("%s %s: status %d, want %d; body %s", method, endpoint, resp.StatusCode, wantCode, body)
	}
	if strings.HasSuffix(endpoint, "/") && method == http.MethodGet && resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected content type: %s", resp.Header.Get("Content-Type"))
	}
	return string(body)
}

func metric(t *testing.T, scrape, name string) float64 {
	t.Helper()
	metricName, requiredLabels := name, ""
	if start := strings.IndexByte(name, '{'); start >= 0 {
		metricName = name[:start]
		requiredLabels = strings.TrimSuffix(name[start+1:], "}")
	}
	for _, line := range strings.Split(scrape, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || (fields[0] != metricName && !strings.HasPrefix(fields[0], metricName+"{")) {
			continue
		}
		matches := true
		for _, label := range strings.Split(requiredLabels, ",") {
			if label != "" && !strings.Contains(fields[0], label) {
				matches = false
				break
			}
		}
		if matches {
			value, err := strconv.ParseFloat(fields[1], 64)
			if err != nil {
				t.Fatal(err)
			}
			return value
		}
	}
	t.Fatalf("metric %s missing from scrape", name)
	return 0
}

func stateRequest(t *testing.T, method, endpoint, body string, wantCode int) string {
	t.Helper()
	req, err := http.NewRequest(method, endpoint, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	response, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != wantCode {
		t.Fatalf("%s %s: status %d, want %d; body %s", method, endpoint, resp.StatusCode, wantCode, response)
	}
	return string(response)
}

func assertMetrics(t *testing.T, scrape string, want map[string]float64) {
	t.Helper()
	for name, expected := range want {
		if got := metric(t, scrape, name); got != expected {
			t.Errorf("%s = %g, want %g", name, got, expected)
		}
	}
}

func TestConfiguration(t *testing.T) {
	s, err := New("", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.role != "api" || s.dependencyURL != "http://dependency:8080/" || s.client.Timeout != dependencyTimeout {
		t.Fatalf("unexpected defaults: %+v", s)
	}
	if _, err := New("worker", ""); err == nil {
		t.Fatal("accepted invalid role")
	}
	for _, upstream := range []string{"relative", "ftp://dependency", "http://", "://bad", "http://user:pass@dependency", "http://dependency/path", "http://dependency?url=x", "http://dependency?", "http://dependency/#fragment"} {
		t.Run(upstream, func(t *testing.T) {
			if _, err := New("api", upstream); err == nil {
				t.Fatal("accepted invalid upstream")
			}
		})
	}
}

func TestRolesAndIndependentRegistries(t *testing.T) {
	dependency := startService(t, "dependency", "")
	api := startService(t, "api", dependency.URL)
	for _, tc := range []struct {
		server *httptest.Server
		role   string
	}{
		{dependency, "dependency"}, {api, "api"},
	} {
		t.Run(tc.role, func(t *testing.T) {
			body := request(t, http.MethodGet, tc.server.URL+"/", 200)
			var response Response
			if err := json.Unmarshal([]byte(body), &response); err != nil {
				t.Fatal(err)
			}
			if response.Role != tc.role {
				t.Fatalf("unexpected response: %s", body)
			}
			if tc.role == "dependency" && response.Dependency != nil {
				t.Fatalf("leaf has a dependency: %s", body)
			}
			if tc.role == "api" && (response.Dependency == nil || *response.Dependency != (DependencyInfo{Role: "dependency", Status: "ok"})) {
				t.Fatalf("API missing dependency success: %s", body)
			}
		})
	}
	apiMetrics := request(t, http.MethodGet, api.URL+"/metrics", 200)
	assertMetrics(t, apiMetrics, map[string]float64{
		`sre_lab_http_requests_total{code="200"}`:           1,
		"sre_lab_http_request_duration_seconds_count":       1,
		"sre_lab_http_active_requests":                      0,
		"sre_lab_dependency_request_duration_seconds_count": 1,
		"sre_lab_dependency_errors_total":                   0,
	})
	for _, name := range []string{"sre_lab_http_request_duration_seconds_sum", "sre_lab_dependency_request_duration_seconds_sum"} {
		if metric(t, apiMetrics, name) <= 0 {
			t.Errorf("%s did not record latency", name)
		}
	}
	for _, bucket := range []string{"0.1", "0.25", "0.5", "1", "2", "5", "+Inf"} {
		metric(t, apiMetrics, `sre_lab_http_request_duration_seconds_bucket{le="`+bucket+`"}`)
		metric(t, apiMetrics, `sre_lab_dependency_request_duration_seconds_bucket{le="`+bucket+`"}`)
	}
	for _, name := range []string{"go_goroutines", "process_cpu_seconds_total"} {
		metric(t, apiMetrics, name)
	}
	assertMetrics(t, request(t, http.MethodGet, dependency.URL+"/metrics", 200), map[string]float64{
		`sre_lab_http_requests_total{code="200"}`:           2,
		"sre_lab_dependency_request_duration_seconds_count": 0,
		"sre_lab_dependency_errors_total":                   0,
	})
}

func TestProbesAndRouting(t *testing.T) {
	// No reachable dependency is needed for liveness or a metrics scrape.
	api := startService(t, "api", "http://127.0.0.1:1")
	for range 2 {
		if body := request(t, http.MethodGet, api.URL+"/healthz", 200); body != "{\"status\":\"ok\"}\n" {
			t.Fatalf("unexpected health: %s", body)
		}
		request(t, http.MethodPost, api.URL+"/healthz", 405)
		request(t, http.MethodPost, api.URL+"/metrics", 405)
		scrape := request(t, http.MethodGet, api.URL+"/metrics", 200)
		assertMetrics(t, scrape, map[string]float64{
			`sre_lab_http_requests_total{code="200"}`:           0,
			`sre_lab_http_requests_total{code="405"}`:           0,
			"sre_lab_http_request_duration_seconds_count":       0,
			"sre_lab_http_active_requests":                      0,
			"sre_lab_dependency_request_duration_seconds_count": 0,
		})
	}
	request(t, http.MethodPost, api.URL+"/", 405)
	for _, path := range []string{"/missing", "/fault", "/proxy", "/healthz/extra"} {
		request(t, http.MethodGet, api.URL+path, 404)
	}
	assertMetrics(t, request(t, http.MethodGet, api.URL+"/metrics", 200), map[string]float64{
		`sre_lab_http_requests_total{code="404"}`:           4,
		`sre_lab_http_requests_total{code="405"}`:           1,
		"sre_lab_http_request_duration_seconds_count":       5,
		"sre_lab_dependency_request_duration_seconds_count": 0,
	})
}

func TestDependencyFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"status", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }},
		{"malformed", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "not JSON") }},
		{"wrong role", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"role":"api"}`) }},
		{"oversized", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, strings.Repeat("x", 65<<10)) }},
		{"header timeout", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }},
		{"body timeout", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			upstream := httptest.NewServer(tc.handler)
			defer upstream.Close()
			api := startService(t, "api", upstream.URL)
			start := time.Now()
			body := request(t, http.MethodGet, api.URL+"/", 502)
			elapsed := time.Since(start)
			if body != "{\"role\":\"api\",\"dependency\":{\"role\":\"dependency\",\"status\":\"error\"}}\n" {
				t.Fatalf("unexpected failure response: %s", body)
			}
			if strings.Contains(tc.name, "timeout") && (elapsed < dependencyTimeout/2 || elapsed > dependencyTimeout+time.Second) {
				t.Errorf("deadline not enforced: %s", elapsed)
			}
			scrape := request(t, http.MethodGet, api.URL+"/metrics", 200)
			assertMetrics(t, scrape, map[string]float64{
				`sre_lab_http_requests_total{code="502"}`:           1,
				`sre_lab_http_requests_total{code="200"}`:           0,
				"sre_lab_http_request_duration_seconds_count":       1,
				"sre_lab_http_active_requests":                      0,
				"sre_lab_dependency_request_duration_seconds_count": 1,
				"sre_lab_dependency_errors_total":                   1,
			})
			if metric(t, scrape, "sre_lab_dependency_request_duration_seconds_sum") <= 0 {
				t.Error("failed call latency not recorded")
			}
		})
	}
	t.Run("connection failure", func(t *testing.T) {
		upstream := httptest.NewServer(http.NotFoundHandler())
		upstream.Close()
		api := startService(t, "api", upstream.URL)
		request(t, http.MethodGet, api.URL+"/", 502)
		assertMetrics(t, request(t, http.MethodGet, api.URL+"/metrics", 200), map[string]float64{
			"sre_lab_dependency_errors_total":         1,
			`sre_lab_http_requests_total{code="502"}`: 1,
		})
	})
}

func TestFixedUpstreamAndNoRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("followed redirect or request-supplied upstream")
	}))
	defer target.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/" {
			t.Errorf("forwarded request input: %s %s", r.Method, r.URL.RequestURI())
		}
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer upstream.Close()
	api := startService(t, "api", upstream.URL)
	request(t, http.MethodGet, api.URL+"/?url="+target.URL, 502)
	assertMetrics(t, request(t, http.MethodGet, api.URL+"/metrics", 200), map[string]float64{
		"sre_lab_dependency_errors_total": 1,
	})
}

func TestActiveRequestsAndCancellation(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()
	api := startService(t, "api", upstream.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("dependency was not called")
	}
	request(t, http.MethodGet, api.URL+"/healthz", 200)
	assertMetrics(t, request(t, http.MethodGet, api.URL+"/metrics", 200), map[string]float64{
		"sre_lab_http_active_requests":                      1,
		"sre_lab_http_request_duration_seconds_count":       0,
		"sre_lab_dependency_request_duration_seconds_count": 0,
	})
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("incoming cancellation did not reach the dependency before its own deadline")
	}
	select {
	case err := <-finished:
		if err == nil {
			t.Error("canceled client request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("client request did not finish")
	}
	deadline := time.Now().Add(time.Second)
	for {
		scrape := request(t, http.MethodGet, api.URL+"/metrics", 200)
		if metric(t, scrape, "sre_lab_http_active_requests") == 0 {
			assertMetrics(t, scrape, map[string]float64{
				`sre_lab_http_requests_total{code="502"}`:           1,
				"sre_lab_http_request_duration_seconds_count":       1,
				"sre_lab_dependency_request_duration_seconds_count": 1,
				"sre_lab_dependency_errors_total":                   1,
			})
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("active request gauge did not return to zero")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStateControlsAndDeterministicFailures(t *testing.T) {
	dependencyCalls := 0
	dependency := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dependencyCalls++
		_, _ = io.WriteString(w, `{"role":"dependency","dependency":null}`)
	}))
	defer dependency.Close()
	api := startService(t, "api", dependency.URL)

	if body := stateRequest(t, http.MethodGet, api.URL+"/admin/state", "", 200); body != "{\"cpu_work\":0,\"failure_every\":0,\"request_sequence\":0}\n" {
		t.Fatalf("unexpected initial state: %s", body)
	}
	stateRequest(t, http.MethodPut, api.URL+"/admin/state", `{"cpu_work":10000,"failure_every":2}`, 200)
	beforeCPUWork := atomic.LoadUint64(&cpuBurnSink)
	request(t, http.MethodGet, api.URL+"/", 200)
	request(t, http.MethodGet, api.URL+"/", 503)
	request(t, http.MethodGet, api.URL+"/", 200)
	if atomic.LoadUint64(&cpuBurnSink) == beforeCPUWork {
		t.Fatal("configured CPU work did not execute")
	}
	if dependencyCalls != 2 {
		t.Fatalf("dependency calls = %d, want 2", dependencyCalls)
	}
	if body := stateRequest(t, http.MethodGet, api.URL+"/admin/state", "", 200); body != "{\"cpu_work\":10000,\"failure_every\":2,\"request_sequence\":3}\n" {
		t.Fatalf("unexpected final state: %s", body)
	}
	scrape := request(t, http.MethodGet, api.URL+"/metrics", 200)
	assertMetrics(t, scrape, map[string]float64{
		"sre_lab_cpu_work":                                  10000,
		"sre_lab_failure_every":                             2,
		`sre_lab_http_requests_total{code="200"}`:           2,
		`sre_lab_http_requests_total{code="503"}`:           1,
		"sre_lab_http_request_duration_seconds_count":       3,
		"sre_lab_dependency_request_duration_seconds_count": 2,
	})
}

func TestStateRejectsInvalidAndExternalRequests(t *testing.T) {
	s, err := New("dependency", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		``, `[]`, `{}`, `{"cpu_work":-1,"failure_every":0}`, `{"cpu_work":20000001,"failure_every":0}`,
		`{"cpu_work":0,"failure_every":10001}`, `{"cpu_work":0,"failure_every":0,"extra":1}`,
		`{"cpu_work":0.5,"failure_every":0}`, `{"cpu_work":null,"failure_every":0}`, `{"cpu_work":0,"failure_every":0} trailing`,
	} {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/admin/state", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		s.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("body %q: status %d, want 400", body, recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/state", nil)
	req.RemoteAddr = "8.8.8.8:53"
	s.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("external request status %d, want 403", recorder.Code)
	}
}

func TestRunIDLabelsAndCPUCancellation(t *testing.T) {
	s, err := New("dependency", "")
	if err != nil {
		t.Fatal(err)
	}
	s.stateMu.Lock()
	s.cpuWork = maxCPUWork
	s.stateMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	req.Header.Set("X-SRE-Run-ID", "exercise_02")
	s.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("canceled CPU work status %d, want 503", recorder.Code)
	}
	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(time.Millisecond, cancel)
	start := time.Now()
	if err := burnCPU(ctx, maxCPUWork); err == nil || time.Since(start) > time.Second {
		t.Fatalf("CPU work did not respond promptly to cancellation: err=%v elapsed=%s", err, time.Since(start))
	}

	server := httptest.NewServer(s)
	defer server.Close()
	requestWithRunID := func(runID string) {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-SRE-Run-ID", runID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("run ID %q: status %d", runID, resp.StatusCode)
		}
	}
	s.stateMu.Lock()
	s.cpuWork = 0
	s.stateMu.Unlock()
	requestWithRunID("exercise_02")
	requestWithRunID(strings.Repeat("x", 33))
	scrape := request(t, http.MethodGet, server.URL+"/metrics", 200)
	metric(t, scrape, `sre_lab_http_requests_total{code="200",run_id="exercise_02"}`)
	metric(t, scrape, `sre_lab_http_request_duration_seconds_count{run_id="exercise_02"}`)
	metric(t, scrape, `sre_lab_http_active_requests{run_id="exercise_02"}`)
	if strings.Contains(scrape, strings.Repeat("x", 33)) {
		t.Fatal("invalid run ID was used as a metric label")
	}
}

func TestCPULimitMetric(t *testing.T) {
	t.Setenv("CPU_LIMIT_CORES", "1.25")
	s, err := New("dependency", "")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s)
	defer server.Close()
	assertMetrics(t, request(t, http.MethodGet, server.URL+"/metrics", 200), map[string]float64{
		"sre_lab_cpu_limit_cores": 1.25,
	})
	t.Setenv("CPU_LIMIT_CORES", "not-a-number")
	if _, err := New("dependency", ""); err == nil {
		t.Fatal("accepted invalid CPU limit")
	}
}
