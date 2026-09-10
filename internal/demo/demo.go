// Package demo provides the Phase 1 API and dependency services.
package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const dependencyTimeout = 2 * time.Second

const (
	defaultCPULimitCores = 0.5
	maxCPUWork           = 20_000_000
	maxFailureEvery      = 10_000
	maxAdminBodySize     = 1024
)

var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

// cpuBurnSink keeps the workload loop observable to the compiler while adding
// negligible synchronization cost relative to configured work.
var cpuBurnSink uint64

// Response is returned by GET / in both roles. Dependency is null for the leaf
// service; an API response reports the outcome of its real dependency call.
type Response struct {
	Role       string          `json:"role"`
	Dependency *DependencyInfo `json:"dependency"`
}

type DependencyInfo struct {
	Role   string `json:"role"`
	Status string `json:"status"`
}

// Service is an HTTP handler with an independent Prometheus registry.
type Service struct {
	role               string
	dependencyURL      string
	client             *http.Client
	metrics            http.Handler
	requests           *prometheus.CounterVec
	duration           *prometheus.HistogramVec
	active             *prometheus.GaugeVec
	dependencyLatency  *prometheus.HistogramVec
	dependencyErrors   *prometheus.CounterVec
	cpuLimit           prometheus.Gauge
	cpuWorkMetric      prometheus.Gauge
	failureEveryMetric prometheus.Gauge
	latencyMetric      prometheus.Gauge
	concurrencyMetric  prometheus.Gauge
	memoryMetric       prometheus.Gauge

	stateMu                sync.RWMutex
	cpuWork                int
	failureEvery           int
	latencyMS              int
	dependencyFailureEvery int
	retries                int
	maxConcurrency         int
	memoryMB               int
	leak                   [][]byte
	requestSeq             atomic.Uint64
	activeCount            atomic.Int64
}

type stateResponse struct {
	CPUWork                int    `json:"cpu_work"`
	FailureEvery           int    `json:"failure_every"`
	LatencyMS              int    `json:"latency_ms,omitempty"`
	DependencyFailureEvery int    `json:"dependency_failure_every,omitempty"`
	Retries                int    `json:"retries,omitempty"`
	MaxConcurrency         int    `json:"max_concurrency,omitempty"`
	MemoryMB               int    `json:"memory_mb,omitempty"`
	RequestSeq             uint64 `json:"request_sequence"`
}

type stateUpdate struct {
	CPUWork                *int `json:"cpu_work"`
	FailureEvery           *int `json:"failure_every"`
	LatencyMS              *int `json:"latency_ms"`
	DependencyFailureEvery *int `json:"dependency_failure_every"`
	Retries                *int `json:"retries"`
	MaxConcurrency         *int `json:"max_concurrency"`
	MemoryMB               *int `json:"memory_mb"`
}

// New validates startup configuration. Empty values default to the API role and
// http://dependency:8080. Only a fixed HTTP(S) origin is accepted as the upstream.
func New(role, dependencyURL string) (*Service, error) {
	if role == "" {
		role = "api"
	}
	if role != "api" && role != "dependency" {
		return nil, fmt.Errorf("ROLE must be api or dependency")
	}
	if dependencyURL == "" {
		dependencyURL = "http://dependency:8080"
	}
	u, err := url.Parse(dependencyURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, fmt.Errorf("DEPENDENCY_URL must be an HTTP(S) origin without credentials, query, or fragment")
	}
	u.Path = "/"
	cpuLimit, err := cpuLimitFromEnv()
	if err != nil {
		return nil, err
	}
	buckets := []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5}
	s := &Service{
		role:          role,
		dependencyURL: u.String(),
		client: &http.Client{
			Timeout: dependencyTimeout,
			// A redirect must not turn this service into an arbitrary URL fetcher.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sre_lab_http_requests_total", Help: "Completed non-probe requests by HTTP status code.",
		}, []string{"code", "run_id"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "sre_lab_http_request_duration_seconds", Help: "Non-probe request duration in seconds.", Buckets: buckets,
		}, []string{"run_id"}),
		active: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "sre_lab_http_active_requests", Help: "In-flight non-probe requests.",
		}, []string{"run_id"}),
		dependencyLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "sre_lab_dependency_request_duration_seconds", Help: "Dependency call duration in seconds, including failures.", Buckets: buckets,
		}, []string{"run_id"}),
		dependencyErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sre_lab_dependency_errors_total", Help: "Failed dependency calls.",
		}, []string{"run_id"}),
		cpuLimit: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sre_lab_cpu_limit_cores", Help: "Configured CPU limit in cores.",
		}),
		cpuWorkMetric: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sre_lab_cpu_work", Help: "Configured CPU work iterations per request.",
		}),
		failureEveryMetric: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "sre_lab_failure_every", Help: "Configured deterministic failure interval; zero disables failures.",
		}),
		latencyMetric:     prometheus.NewGauge(prometheus.GaugeOpts{Name: "sre_lab_configured_latency_ms", Help: "Configured deterministic latency in milliseconds."}),
		concurrencyMetric: prometheus.NewGauge(prometheus.GaugeOpts{Name: "sre_lab_configured_max_concurrency", Help: "Configured maximum concurrent requests; zero disables the limit."}),
		memoryMetric:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "sre_lab_configured_memory_leak_mb", Help: "Configured memory growth per request in megabytes."}),
	}
	// These are the only status codes emitted by the non-probe handler.
	for _, code := range []string{"200", "404", "405", "502", "503"} {
		s.requests.WithLabelValues(code, "manual")
	}
	s.duration.WithLabelValues("manual")
	s.active.WithLabelValues("manual")
	s.dependencyLatency.WithLabelValues("manual")
	s.dependencyErrors.WithLabelValues("manual")
	s.cpuLimit.Set(cpuLimit)
	s.cpuWorkMetric.Set(0)
	s.failureEveryMetric.Set(0)
	s.latencyMetric.Set(0)
	s.concurrencyMetric.Set(0)
	s.memoryMetric.Set(0)
	registry := prometheus.NewRegistry()
	registry.MustRegister(s.requests, s.duration, s.active, s.dependencyLatency, s.dependencyErrors, s.cpuLimit, s.cpuWorkMetric, s.failureEveryMetric, s.latencyMetric, s.concurrencyMetric, s.memoryMetric,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	s.metrics = promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	return s, nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/metrics" {
			s.metrics.ServeHTTP(w, r)
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "{\"status\":\"ok\"}\n")
		}
		return
	}
	if r.URL.Path == "/admin/state" {
		s.serveState(w, r)
		return
	}
	runID := requestRunID(r)
	start := time.Now()
	s.active.WithLabelValues(runID).Inc()
	s.activeCount.Add(1)
	defer s.activeCount.Add(-1)
	code := http.StatusOK
	defer func() {
		s.requests.WithLabelValues(strconv.Itoa(code), runID).Inc()
		s.duration.WithLabelValues(runID).Observe(time.Since(start).Seconds())
		s.active.WithLabelValues(runID).Dec()
	}()
	if r.URL.Path != "/" {
		code = http.StatusNotFound
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		code = http.StatusMethodNotAllowed
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", code)
		return
	}
	cpuWork, failureEvery := s.workload()
	latencyMS, dependencyFailureEvery, retries, maxConcurrency, memoryMB := s.settings()
	if maxConcurrency > 0 && int(s.activeValue(runID)) > maxConcurrency {
		code = http.StatusServiceUnavailable
		writeJSON(w, code, Response{Role: s.role})
		return
	}
	if err := waitLatency(r.Context(), latencyMS); err != nil {
		code = http.StatusServiceUnavailable
		writeJSON(w, code, Response{Role: s.role})
		return
	}
	if memoryMB > 0 {
		s.growMemory(memoryMB)
	}
	sequence := s.requestSeq.Add(1)
	if s.role == "dependency" && dependencyFailureEvery > 0 && sequence%uint64(dependencyFailureEvery) == 0 {
		code = http.StatusServiceUnavailable
		writeJSON(w, code, Response{Role: s.role})
		return
	}
	if failureEvery > 0 && sequence%uint64(failureEvery) == 0 {
		code = http.StatusServiceUnavailable
		writeJSON(w, code, Response{Role: s.role})
		return
	}
	if err := burnCPU(r.Context(), cpuWork); err != nil {
		code = http.StatusServiceUnavailable
		writeJSON(w, code, Response{Role: s.role})
		return
	}
	response := Response{Role: s.role}
	if s.role == "api" {
		response.Dependency = &DependencyInfo{Role: "dependency", Status: "ok"}
		if err := s.callDependency(r.Context(), runID, dependencyFailureEvery, retries); err != nil {
			code = http.StatusBadGateway
			response.Dependency.Status = "error"
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Service) callDependency(parent context.Context, runID string, dependencyFailureEvery, retries int) (err error) {
	start := time.Now()
	defer func() {
		s.dependencyLatency.WithLabelValues(runID).Observe(time.Since(start).Seconds())
		if err != nil {
			s.dependencyErrors.WithLabelValues(runID).Inc()
		}
	}()
	ctx, cancel := context.WithTimeout(parent, dependencyTimeout)
	defer cancel()
	for attempt := 0; attempt <= retries; attempt++ {
		req, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, s.dependencyURL, nil)
		if requestErr != nil {
			return requestErr
		}
		if runID != "manual" {
			req.Header.Set("X-SRE-Run-ID", runID)
		}
		resp, requestErr := s.client.Do(req)
		if requestErr != nil {
			err = requestErr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			err = fmt.Errorf("dependency returned HTTP %d", resp.StatusCode)
			continue
		}
		// Bound both body size and read time; a successful status alone is not proof
		// that the dependency returned a valid demo response.
		const maxBody = 64 << 10
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
		resp.Body.Close()
		if readErr != nil {
			err = readErr
			continue
		}
		if len(body) > maxBody {
			err = fmt.Errorf("dependency response exceeds size limit")
			continue
		}
		var response Response
		if decodeErr := json.Unmarshal(body, &response); decodeErr != nil {
			err = decodeErr
			continue
		}
		if response.Role != "dependency" || response.Dependency != nil {
			err = fmt.Errorf("unexpected dependency response")
			continue
		}
		return nil
	}
	return err
}

func cpuLimitFromEnv() (float64, error) {
	value := os.Getenv("CPU_LIMIT_CORES")
	if value == "" {
		return defaultCPULimitCores, nil
	}
	limit, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(limit) || math.IsInf(limit, 0) || limit <= 0 {
		return 0, fmt.Errorf("CPU_LIMIT_CORES must be a positive finite number")
	}
	return limit, nil
}

func (s *Service) serveState(w http.ResponseWriter, r *http.Request) {
	if !isInternalRequest(r) {
		http.Error(w, "admin endpoint requires an internal client", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodGet:
		cpuWork, failureEvery := s.workload()
		writeJSON(w, http.StatusOK, s.stateResponse(cpuWork, failureEvery))
	case http.MethodPut:
		var update stateUpdate
		decoder := json.NewDecoder(io.LimitReader(r.Body, maxAdminBodySize+1))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&update); err != nil || update.CPUWork == nil || update.FailureEvery == nil || decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid state JSON", http.StatusBadRequest)
			return
		}
		if *update.CPUWork < 0 || *update.CPUWork > maxCPUWork || *update.FailureEvery < 0 || *update.FailureEvery > maxFailureEvery || update.LatencyMS != nil && (*update.LatencyMS < 0 || *update.LatencyMS > 10000) || update.DependencyFailureEvery != nil && (*update.DependencyFailureEvery < 0 || *update.DependencyFailureEvery > maxFailureEvery) || update.Retries != nil && (*update.Retries < 0 || *update.Retries > 10) || update.MaxConcurrency != nil && (*update.MaxConcurrency < 0 || *update.MaxConcurrency > 1000) || update.MemoryMB != nil && (*update.MemoryMB < 0 || *update.MemoryMB > 1024) {
			http.Error(w, "state values out of range", http.StatusBadRequest)
			return
		}
		s.stateMu.Lock()
		s.cpuWork = *update.CPUWork
		s.failureEvery = *update.FailureEvery
		if update.LatencyMS != nil {
			s.latencyMS = *update.LatencyMS
		}
		if update.DependencyFailureEvery != nil {
			s.dependencyFailureEvery = *update.DependencyFailureEvery
		}
		if update.Retries != nil {
			s.retries = *update.Retries
		}
		if update.MaxConcurrency != nil {
			s.maxConcurrency = *update.MaxConcurrency
		}
		if update.MemoryMB != nil {
			s.memoryMB = *update.MemoryMB
			if s.memoryMB == 0 {
				s.leak = nil
			}
		}
		s.cpuWorkMetric.Set(float64(s.cpuWork))
		s.failureEveryMetric.Set(float64(s.failureEvery))
		s.latencyMetric.Set(float64(s.latencyMS))
		s.concurrencyMetric.Set(float64(s.maxConcurrency))
		s.memoryMetric.Set(float64(s.memoryMB))
		s.stateMu.Unlock()
		writeJSON(w, http.StatusOK, s.stateResponse(*update.CPUWork, *update.FailureEvery))
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Service) stateResponse(cpuWork, failureEvery int) stateResponse {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return stateResponse{CPUWork: cpuWork, FailureEvery: failureEvery, LatencyMS: s.latencyMS, DependencyFailureEvery: s.dependencyFailureEvery, Retries: s.retries, MaxConcurrency: s.maxConcurrency, MemoryMB: s.memoryMB, RequestSeq: s.requestSeq.Load()}
}

func (s *Service) workload() (int, int) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.cpuWork, s.failureEvery
}

func (s *Service) settings() (int, int, int, int, int) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.latencyMS, s.dependencyFailureEvery, s.retries, s.maxConcurrency, s.memoryMB
}
func (s *Service) activeValue(_ string) int64 { return s.activeCount.Load() }
func (s *Service) growMemory(megabytes int) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if len(s.leak) < 256 {
		s.leak = append(s.leak, make([]byte, megabytes<<20))
	}
}
func waitLatency(ctx context.Context, milliseconds int) error {
	if milliseconds <= 0 {
		return nil
	}
	timer := time.NewTimer(time.Duration(milliseconds) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func isInternalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func requestRunID(r *http.Request) string {
	runID := r.Header.Get("X-SRE-Run-ID")
	if runID == "" || !runIDPattern.MatchString(runID) {
		return "manual"
	}
	return runID
}

func burnCPU(ctx context.Context, work int) error {
	var value uint64 = 1
	for i := 0; i < work; i++ {
		if i%1024 == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}
		value = value*6364136223846793005 + 1442695040888963407
	}
	atomic.AddUint64(&cpuBurnSink, value)
	return nil
}
