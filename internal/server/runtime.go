package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"sre-lab/internal/history"
	"sre-lab/internal/scenario"
)

type runtimeConfig struct {
	Root          string
	TrafficURL    string
	PromURL       string
	LBURL         string
	API1URL       string
	API2URL       string
	DependencyURL string
	HistoryPath   string
}

type runState struct {
	ID               string    `json:"runID"`
	Scenario         string    `json:"scenario"`
	State            string    `json:"state"`
	Phase            string    `json:"phase"`
	StartedAt        time.Time `json:"startedAt"`
	RevealedHints    []int     `json:"revealedHints"`
	AlertExpression  string    `json:"alertExpression,omitempty"`
	AlertFired       bool      `json:"alertFired"`
	AlertCleared     bool      `json:"alertCleared"`
	AlertBaseline    bool      `json:"alertBaseline"`
	IncidentSeen     bool      `json:"incidentSeen"`
	RecoverySeen     bool      `json:"recoverySeen"`
	Interventions    []string  `json:"interventions,omitempty"`
	FileEvidenceSeen bool      `json:"fileEvidenceSeen"`
}

type runtimeManager struct {
	mu        sync.Mutex
	scenarios []scenario.Scenario
	byID      map[string]scenario.Scenario
	config    runtimeConfig
	client    *http.Client
	active    *runState
	history   *history.Store
}

func newRuntime(c runtimeConfig) (*runtimeManager, error) {
	if c.Root == "" {
		return nil, errors.New("scenario root is required")
	}
	list, err := scenario.Load(c.Root)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]scenario.Scenario, len(list))
	for _, item := range list {
		byID[item.ID] = item
	}
	store, err := history.Open(c.HistoryPath)
	if err != nil {
		return nil, err
	}
	return &runtimeManager{scenarios: list, byID: byID, config: c, client: &http.Client{Timeout: 5 * time.Second}, history: store}, nil
}

func (m *runtimeManager) list() []scenario.Scenario {
	return append([]scenario.Scenario(nil), m.scenarios...)
}

func (m *runtimeManager) status() any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return map[string]any{"state": "idle"}
	}
	copy := *m.active
	copy.RevealedHints = append([]int(nil), m.active.RevealedHints...)
	copy.Interventions = append([]string(nil), m.active.Interventions...)
	return copy
}

func (m *runtimeManager) start(ctx context.Context, id string) (runState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil {
		return runState{}, fmt.Errorf("a run is already active")
	}
	item, ok := m.byID[id]
	if !ok {
		return runState{}, fmt.Errorf("unknown scenario")
	}
	if err := m.stopTraffic(ctx); err != nil {
		return runState{}, err
	}
	if err := m.applyState(ctx, "baseline"); err != nil {
		return runState{}, err
	}
	run := &runState{ID: newRunID(), Scenario: item.ID, State: "starting", Phase: "baseline", StartedAt: time.Now().UTC()}
	m.active = run
	if item.ID != "useful-alerts" && item.ID != "slo-burn-rate" {
		if err := m.applyState(ctx, "incident"); err != nil {
			m.active = nil
			return runState{}, err
		}
	}
	if err := m.startTraffic(ctx, run); err != nil {
		_ = m.applyState(context.Background(), "baseline")
		m.active = nil
		return runState{}, err
	}
	run.State = "running"
	return *run, nil
}

func (m *runtimeManager) reset(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.stopTraffic(ctx); err != nil {
		return err
	}
	if err := m.applyState(ctx, "baseline"); err != nil {
		return err
	}
	m.active = nil
	return nil
}

func (m *runtimeManager) setCapacity(ctx context.Context, backends int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || (m.active.Scenario != "cpu-saturation" && m.active.Scenario != "vertical-horizontal") {
		return errors.New("CPU saturation run is not active")
	}
	if backends != 1 && backends != 2 {
		return errors.New("active_backends must be 1 or 2")
	}
	if err := m.putJSON(ctx, m.config.LBURL+"/admin/state", map[string]int{"active_backends": backends}, nil); err != nil {
		return err
	}
	if backends == 2 {
		m.active.Interventions = append(m.active.Interventions, "horizontal-capacity")
		m.active.Phase = "recovery"
		m.active.RecoverySeen = true
	}
	return nil
}

func (m *runtimeManager) intervene(ctx context.Context, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return errors.New("no active run")
	}
	allowed := map[string]string{
		"dependency-recovery": "dependency-bottleneck",
		"pool-recovery":       "connection-pool",
		"latency-budget":      "latency-slo",
		"dns-recovery":        "dns-failure",
		"retry-budget":        "retry-storm",
		"replace-capacity":    "memory-leak",
		"stabilize-policy":    "autoscaler-oscillation",
	}
	if allowed[action] != m.active.Scenario {
		return errors.New("intervention is not valid for this exercise")
	}
	if err := m.applyState(ctx, "recovery"); err != nil {
		return err
	}
	m.active.Phase = "recovery"
	m.active.RecoverySeen = true
	m.active.Interventions = append(m.active.Interventions, action)
	return nil
}

func (m *runtimeManager) setPhase(ctx context.Context, phase string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return errors.New("no active run")
	}
	if phase != "baseline" && phase != "incident" && phase != "recovery" {
		return errors.New("phase must be baseline, incident, or recovery")
	}
	if err := m.applyState(ctx, phase); err != nil {
		return err
	}
	m.active.Phase = phase
	if phase == "incident" {
		m.active.IncidentSeen = true
		m.scheduleAlertEvaluation(m.active.ID, "incident")
	}
	if phase == "recovery" {
		m.active.RecoverySeen = true
		m.scheduleAlertEvaluation(m.active.ID, "recovery")
	}
	return nil
}

func (m *runtimeManager) scheduleAlertEvaluation(runID, phase string) {
	go func() {
		// Allow a request burst and at least one Prometheus scrape to represent the
		// newly selected phase before evaluating the learner's expression.
		time.Sleep(6 * time.Second)
		m.mu.Lock()
		if m.active == nil || m.active.ID != runID || m.active.AlertExpression == "" {
			m.mu.Unlock()
			return
		}
		expression := m.active.AlertExpression
		m.mu.Unlock()
		fired := m.evaluateAlert(context.Background(), expression)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.active == nil || m.active.ID != runID {
			return
		}
		if phase == "incident" {
			m.active.AlertFired = fired
		} else if phase == "recovery" {
			m.active.AlertCleared = !fired
		} else {
			m.active.AlertBaseline = fired
		}
	}()
}

func (m *runtimeManager) revealHint() ([]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return nil, errors.New("no active run")
	}
	item := m.byID[m.active.Scenario]
	next := len(m.active.RevealedHints)
	if next < len(item.Hints) {
		m.active.RevealedHints = append(m.active.RevealedHints, next)
	}
	return append([]int(nil), m.active.RevealedHints...), nil
}

func (m *runtimeManager) setAlert(ctx context.Context, expression string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || (m.active.Scenario != "useful-alerts" && m.active.Scenario != "slo-burn-rate") {
		return errors.New("an alert exercise is not active")
	}
	if len(expression) == 0 || len(expression) > 500 || strings.ContainsAny(expression, ";\n\r") {
		return errors.New("alert expression is empty or contains unsupported characters")
	}
	if _, err := m.query(ctx, expression); err != nil {
		return fmt.Errorf("invalid PromQL: %w", err)
	}
	m.active.AlertExpression = expression
	if m.active.Phase == "baseline" {
		m.scheduleAlertEvaluation(m.active.ID, "baseline")
	}
	return nil
}

func (m *runtimeManager) check(ctx context.Context) map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return map[string]any{"passed": false, "state": "idle", "feedback": "Start an exercise first."}
	}
	availability, p95, count := m.serviceEvidence(ctx, m.active.ID)
	item := m.byID[m.active.Scenario]
	result := map[string]any{"passed": false, "scenario": m.active.Scenario, "availability": availability, "p95_latency_ms": p95, "requests": count, "objective_availability": item.Objectives.Availability, "objective_p95_latency_ms": item.Objectives.P95LatencyMS, "minimum_requests": item.Grading.MinimumRequests}
	switch m.active.Scenario {
	case "cpu-saturation":
		result["passed"] = hasIntervention(m.active, "horizontal-capacity") && m.active.RecoverySeen && availability >= item.Objectives.Availability && p95 <= float64(item.Objectives.P95LatencyMS) && count >= float64(item.Grading.MinimumRequests)
		result["feedback"] = "Increase API capacity, mark recovery, and sustain the declared availability, latency, and request window."
	case "useful-alerts":
		result["passed"] = m.active.AlertExpression != "" && !m.active.AlertBaseline && m.active.IncidentSeen && m.active.RecoverySeen && m.active.AlertFired && m.active.AlertCleared
		result["feedback"] = "A useful alert must stay quiet in baseline, evaluate during user impact, and clear after recovery."
	case "slo-burn-rate":
		result["passed"] = m.active.AlertExpression != "" && !m.active.AlertBaseline && m.active.IncidentSeen && m.active.RecoverySeen && m.active.AlertFired && m.active.AlertCleared
		result["feedback"] = "Define the valid-request SLI, observe rapid burn, and verify the alert clears after recovery."
	case "vertical-horizontal":
		result["passed"] = hasIntervention(m.active, "horizontal-capacity") && m.active.RecoverySeen && availability >= item.Objectives.Availability && p95 <= float64(item.Objectives.P95LatencyMS) && count >= float64(item.Grading.MinimumRequests)
		result["feedback"] = "Compare vertical and horizontal capacity, select the healthier API pool, mark recovery, and verify the declared SLO."
	case "dependency-bottleneck":
		result["passed"] = hasIntervention(m.active, "dependency-recovery") && m.active.RecoverySeen && availability >= item.Objectives.Availability && p95 <= float64(item.Objectives.P95LatencyMS) && count >= float64(item.Grading.MinimumRequests)
		result["feedback"] = "Confirm dependency latency is the bottleneck, restore the dependency path, then verify the latency objective."
	case "connection-pool":
		result["passed"] = hasIntervention(m.active, "pool-recovery") && m.active.RecoverySeen && availability >= item.Objectives.Availability && p95 <= float64(item.Objectives.P95LatencyMS) && count >= float64(item.Grading.MinimumRequests)
		result["feedback"] = "Identify pool wait or exhaustion, restore usable concurrency, and verify recovery under sustained traffic."
	case "latency-slo":
		result["passed"] = hasIntervention(m.active, "latency-budget") && m.active.RecoverySeen && availability >= item.Objectives.Availability && p95 <= float64(item.Objectives.P95LatencyMS) && count >= float64(item.Grading.MinimumRequests)
		result["feedback"] = "Separate latency-budget failure from availability failure and verify the declared latency SLO after recovery."
	case "dns-failure":
		result["passed"] = hasIntervention(m.active, "dns-recovery") && m.active.RecoverySeen && availability >= item.Objectives.Availability && p95 <= float64(item.Objectives.P95LatencyMS) && count >= float64(item.Grading.MinimumRequests)
		result["feedback"] = "Use request evidence to distinguish name-resolution failure from application failure, then verify recovery."
	case "retry-storm":
		result["passed"] = hasIntervention(m.active, "retry-budget") && m.active.RecoverySeen && availability >= item.Objectives.Availability && p95 <= float64(item.Objectives.P95LatencyMS) && count >= float64(item.Grading.MinimumRequests)
		result["feedback"] = "Find retry amplification in request and dependency evidence, stop the amplification, and verify recovery."
	case "memory-leak":
		result["passed"] = hasIntervention(m.active, "replace-capacity") && m.active.RecoverySeen && availability >= item.Objectives.Availability && p95 <= float64(item.Objectives.P95LatencyMS) && count >= float64(item.Grading.MinimumRequests)
		result["feedback"] = "Correlate memory growth with traffic and restart or replace the unhealthy capacity before verifying recovery."
	case "autoscaler-oscillation":
		result["passed"] = hasIntervention(m.active, "stabilize-policy") && m.active.RecoverySeen && availability >= item.Objectives.Availability && p95 <= float64(item.Objectives.P95LatencyMS) && count >= float64(item.Grading.MinimumRequests)
		result["feedback"] = "Identify unstable capacity changes, stabilize the policy, mark recovery, and verify the declared SLO."
	case "file-forensics":
		result["passed"] = m.active.FileEvidenceSeen
		result["feedback"] = "Use lsof to identify the open file, then map the file descriptor to the responsible file-reader process."
	}
	passed, _ := result["passed"].(bool)
	feedback, _ := result["feedback"].(string)
	_ = m.history.Add(history.Record{RunID: m.active.ID, Scenario: m.active.Scenario, StartedAt: m.active.StartedAt, CheckedAt: time.Now().UTC(), Passed: passed, Feedback: feedback, Availability: availability, P95MS: p95})
	return result
}

func (m *runtimeManager) markFileEvidence() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil || m.active.Scenario != "file-forensics" {
		return errors.New("file forensics exercise is not active")
	}
	m.active.FileEvidenceSeen = true
	return nil
}

func hasIntervention(run *runState, action string) bool {
	for _, item := range run.Interventions {
		if item == action {
			return true
		}
	}
	return false
}

func (m *runtimeManager) serviceEvidence(ctx context.Context, runID string) (float64, float64, float64) {
	window := "1m"
	good, _ := m.query(ctx, fmt.Sprintf(`sum(increase(sre_lab_http_requests_total{run_id=%q,code="200"}[%s]))`, runID, window))
	total, _ := m.query(ctx, fmt.Sprintf(`sum(increase(sre_lab_http_requests_total{run_id=%q}[%s]))`, runID, window))
	p95, _ := m.query(ctx, fmt.Sprintf(`histogram_quantile(0.95, sum(rate(sre_lab_http_request_duration_seconds_bucket{run_id=%q}[%s])) by (le))`, runID, window))
	availability := 0.0
	if total > 0 {
		availability = good / total
	}
	return availability, p95 * 1000, total
}

func (m *runtimeManager) evaluateAlert(ctx context.Context, expression string) bool {
	if expression == "" {
		return false
	}
	value, err := m.query(ctx, expression)
	return err == nil && value != 0
}

func (m *runtimeManager) query(ctx context.Context, expression string) (float64, error) {
	u, err := url.Parse(m.config.PromURL + "/api/v1/query")
	if err != nil {
		return 0, err
	}
	q := u.Query()
	q.Set("query", expression)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) != nil || body.Status != "success" {
		return 0, errors.New("Prometheus query failed")
	}
	if len(body.Data.Result) == 0 || len(body.Data.Result[0].Value) < 2 {
		return 0, nil
	}
	var raw string
	if err := json.Unmarshal(body.Data.Result[0].Value[1], &raw); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(raw, 64)
}

func (m *runtimeManager) startTraffic(ctx context.Context, run *runState) error {
	profile := map[string]string{"cpu-saturation": "cpu", "useful-alerts": "alert", "slo-burn-rate": "slo", "vertical-horizontal": "cpu", "dependency-bottleneck": "alert", "connection-pool": "alert", "latency-slo": "slo", "dns-failure": "alert", "retry-storm": "alert", "memory-leak": "alert", "autoscaler-oscillation": "cpu", "file-forensics": "alert"}[run.Scenario]
	return m.requestJSON(ctx, http.MethodPost, m.config.TrafficURL+"/start", map[string]string{"runID": run.ID, "scenario": run.Scenario, "profile": profile}, nil)
}

func (m *runtimeManager) stopTraffic(ctx context.Context) error {
	return m.requestJSON(ctx, http.MethodPost, m.config.TrafficURL+"/stop", nil, nil)
}

func (m *runtimeManager) applyState(ctx context.Context, phase string) error {
	cpu, failures, latency, depFailures, retries, maxConcurrency, memoryMB := 0, 0, 0, 0, 0, 0, 0
	scenarioID := ""
	if m.active != nil {
		scenarioID = m.active.Scenario
	}
	if phase == "incident" {
		switch scenarioID {
		case "cpu-saturation", "vertical-horizontal", "autoscaler-oscillation":
			cpu = 1000000
		case "useful-alerts":
			cpu, failures = 2000000, 5
		case "dependency-bottleneck":
			latency = 300
		case "connection-pool":
			maxConcurrency = 4
		case "latency-slo":
			latency = 300
		case "dns-failure":
			depFailures = 1
		case "retry-storm":
			retries, depFailures = 3, 5
		case "memory-leak":
			memoryMB = 2
		case "slo-burn-rate":
			failures = 5
		}
	}
	apiState := map[string]int{"cpu_work": cpu, "failure_every": failures, "latency_ms": 0, "dependency_failure_every": 0, "retries": retries, "max_concurrency": maxConcurrency, "memory_mb": memoryMB}
	for _, endpoint := range []string{m.config.API1URL, m.config.API2URL} {
		if err := m.putJSON(ctx, endpoint+"/admin/state", apiState, nil); err != nil {
			return err
		}
	}
	dependencyState := map[string]int{"cpu_work": 0, "failure_every": 0, "latency_ms": latency, "dependency_failure_every": depFailures, "retries": 0, "max_concurrency": 0, "memory_mb": 0}
	if err := m.putJSON(ctx, m.config.DependencyURL+"/admin/state", dependencyState, nil); err != nil {
		return err
	}
	backends := 2
	if scenarioID == "cpu-saturation" || scenarioID == "vertical-horizontal" || scenarioID == "autoscaler-oscillation" {
		backends = 1
	}
	return m.putJSON(ctx, m.config.LBURL+"/admin/state", map[string]int{"active_backends": backends}, nil)
}

func (m *runtimeManager) putJSON(ctx context.Context, endpoint string, payload any, result any) error {
	return m.requestJSON(ctx, http.MethodPut, endpoint, payload, result)
}

func (m *runtimeManager) requestJSON(ctx context.Context, method, endpoint string, payload any, result any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(data))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("control endpoint %s returned HTTP %d: %s", endpoint, resp.StatusCode, strings.TrimSpace(string(message)))
	}
	if result != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result)
	}
	return nil
}

func newRunID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "run-local"
	}
	return "run-" + hex.EncodeToString(b)
}
