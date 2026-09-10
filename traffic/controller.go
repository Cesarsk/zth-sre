// Package traffic manages the single k6 process used to generate lab traffic.
package traffic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"syscall"
	"time"
)

const (
	defaultK6Path            = "/usr/bin/k6"
	fixedScriptPath          = "/app/traffic/script.js"
	defaultTargetURL         = "http://api-lb:8080"
	defaultPrometheusRWURL   = "http://prometheus:9090/api/v1/write"
	stopTimeout              = 10 * time.Second
	maxRunDuration           = 6 * time.Minute
	maxStartRequestBodyBytes = 4096
)

var (
	runIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	profiles     = map[string]string{
		"cpu-saturation": "cpu",
		"useful-alerts":  "alert",
		"alerting":       "alert",
		"slo-burn-rate":  "slo",
		"vertical-horizontal": "cpu",
		"dependency-bottleneck": "alert",
		"connection-pool": "alert",
		"latency-slo": "slo",
		"dns-failure": "alert",
		"retry-storm": "alert",
		"memory-leak": "alert",
		"autoscaler-oscillation": "cpu",
	}
)

type Config struct {
	K6Path          string
	TargetURL       string
	PrometheusRWURL string
}

func ConfigFromEnv() Config {
	return Config{
		K6Path:          env("K6_PATH", defaultK6Path),
		TargetURL:       env("TARGET_URL", defaultTargetURL),
		PrometheusRWURL: env("PROMETHEUS_RW_URL", defaultPrometheusRWURL),
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

type Controller struct {
	config Config

	mu     sync.Mutex
	active *run
	closed bool
}

type run struct {
	cmd      *exec.Cmd
	runID    string
	scenario string
	profile  string
	done     chan struct{}
	timer    *time.Timer
}

type startRequest struct {
	RunID    string `json:"runID"`
	Scenario string `json:"scenario"`
	Profile  string `json:"profile"`
}

type statusResponse struct {
	Status   string `json:"status"`
	RunID    string `json:"runID,omitempty"`
	Scenario string `json:"scenario,omitempty"`
	Profile  string `json:"profile,omitempty"`
	PID      int    `json:"pid,omitempty"`
}

func New(config Config) *Controller {
	if config.K6Path == "" {
		config.K6Path = defaultK6Path
	}
	if config.TargetURL == "" {
		config.TargetURL = defaultTargetURL
	}
	if config.PrometheusRWURL == "" {
		config.PrometheusRWURL = defaultPrometheusRWURL
	}
	return &Controller{config: config}
}

func (c *Controller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	case "/status":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, http.StatusOK, c.status())
	case "/start":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		c.handleStart(w, r)
	case "/stop":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		c.handleStop(w)
	default:
		http.NotFound(w, r)
	}
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func (c *Controller) handleStart(w http.ResponseWriter, r *http.Request) {
	if !isJSON(r.Header.Get("Content-Type")) {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxStartRequestBodyBytes))
	decoder.DisallowUnknownFields()
	var request startRequest
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !validRequest(request) {
		http.Error(w, "invalid start request", http.StatusBadRequest)
		return
	}

	status, err := c.start(request)
	if errors.Is(err, errRunActive) {
		writeJSON(w, http.StatusConflict, status)
		return
	}
	if errors.Is(err, errClosed) {
		http.Error(w, "controller is shutting down", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, "unable to start k6", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, status)
}

func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}

func validRequest(request startRequest) bool {
	return runIDPattern.MatchString(request.RunID) && profiles[request.Scenario] == request.Profile
}

var (
	errRunActive = errors.New("k6 run already active")
	errClosed    = errors.New("controller closed")
)

func (c *Controller) start(request startRequest) (statusResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return statusResponse{}, errClosed
	}
	if c.active != nil {
		return c.statusLocked(), errRunActive
	}

	cmd := exec.Command(c.config.K6Path,
		"run",
		"--out", "experimental-prometheus-rw="+c.config.PrometheusRWURL,
		"--tag", "source=sre-lab",
		"--tag", "run_id="+request.RunID,
		"--tag", "scenario="+request.Scenario,
		"--tag", "profile="+request.Profile,
		"--env", "RUN_ID="+request.RunID,
		"--env", "SCENARIO="+request.Scenario,
		"--env", "PROFILE="+request.Profile,
		"--env", "TARGET_URL="+c.config.TargetURL,
		fixedScriptPath,
	)
	cmd.Env = append(os.Environ(),
		"RUN_ID="+request.RunID,
		"SCENARIO="+request.Scenario,
		"PROFILE="+request.Profile,
		"TARGET_URL="+c.config.TargetURL,
	)
	// A separate group lets stop terminate k6 and any workers it creates.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return statusResponse{}, err
	}

	r := &run{cmd: cmd, runID: request.RunID, scenario: request.Scenario, profile: request.Profile, done: make(chan struct{})}
	c.active = r
	r.timer = time.AfterFunc(maxRunDuration, func() {
		ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		_ = c.stopRun(ctx, r)
	})
	go c.reap(r)
	return c.statusLocked(), nil
}

func (c *Controller) reap(r *run) {
	_ = r.cmd.Wait()
	r.timer.Stop()
	c.mu.Lock()
	if c.active == r {
		c.active = nil
	}
	c.mu.Unlock()
	close(r.done)
}

func (c *Controller) handleStop(w http.ResponseWriter) {
	c.mu.Lock()
	r := c.active
	c.mu.Unlock()
	if r == nil {
		writeJSON(w, http.StatusOK, statusResponse{Status: "idle"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	if err := c.stopRun(ctx, r); err != nil {
		http.Error(w, "unable to stop k6", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, c.status())
}

func (c *Controller) stopRun(ctx context.Context, r *run) error {
	select {
	case <-r.done:
		return nil
	default:
	}
	if err := signalGroup(r.cmd.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		if err := signalGroup(r.cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		<-r.done
		return nil
	}
}

func signalGroup(pid int, signal syscall.Signal) error {
	return syscall.Kill(-pid, signal)
}

func (c *Controller) status() statusResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statusLocked()
}

func (c *Controller) statusLocked() statusResponse {
	if c.active == nil {
		return statusResponse{Status: "idle"}
	}
	return statusResponse{
		Status:   "running",
		RunID:    c.active.runID,
		Scenario: c.active.scenario,
		Profile:  c.active.profile,
		PID:      c.active.cmd.Process.Pid,
	}
}

// Shutdown prevents subsequent starts and reaps an active k6 process.
func (c *Controller) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true
	r := c.active
	c.mu.Unlock()
	if r == nil {
		return nil
	}
	return c.stopRun(ctx, r)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
