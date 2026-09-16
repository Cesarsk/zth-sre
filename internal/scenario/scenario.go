// Package scenario loads and validates installed Phase 2 exercise definitions.
package scenario

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v2"
)

const (
	SchemaVersion = 1
	maxRate       = 1000
	maxDuration   = time.Hour
)

var (
	validID     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	requiredIDs = map[string]bool{
		"cpu-saturation":         true,
		"useful-alerts":          true,
		"slo-burn-rate":          true,
		"vertical-horizontal":    true,
		"dependency-bottleneck":  true,
		"connection-pool":        true,
		"latency-slo":            true,
		"dns-failure":            true,
		"retry-storm":            true,
		"memory-leak":            true,
		"autoscaler-oscillation": true,
		"file-forensics":         true,
		"blocked-traffic":        true,
	}
)

// Metadata is the stable, learner-facing identity of a scenario.
type Metadata struct {
	SchemaVersion      int      `yaml:"schema_version"`
	ID                 string   `yaml:"id"`
	Title              string   `yaml:"title"`
	Difficulty         string   `yaml:"difficulty"`
	Description        string   `yaml:"description"`
	LearningObjectives []string `yaml:"learning_objectives"`
}

// Scenario is an installed, declarative exercise definition. It intentionally
// describes desired conditions rather than commands or provider implementation.
type Scenario struct {
	Metadata        `yaml:",inline"`
	Goal            string      `yaml:"goal"`
	SuccessCriteria []string    `yaml:"success_criteria"`
	Prerequisites   []string    `yaml:"prerequisites"`
	RequiredTools   []string    `yaml:"required_tools"`
	FalseHypotheses []string    `yaml:"false_hypotheses"`
	Environment     Environment `yaml:"environment"`
	Traffic         Traffic     `yaml:"traffic"`
	Faults          []Fault     `yaml:"faults"`
	Objectives      Objectives  `yaml:"objectives"`
	Grading         Grading     `yaml:"grading"`
	Hints           []string    `yaml:"hints"`
	Diagram         Diagram     `yaml:"diagram"`
}

type Diagram struct {
	Nodes []DiagramNode `yaml:"nodes" json:"nodes"`
	Edges []DiagramEdge `yaml:"edges" json:"edges"`
}

type DiagramNode struct {
	ID    string `yaml:"id" json:"id"`
	Label string `yaml:"label" json:"label"`
	Role  string `yaml:"role" json:"role"`
}

type DiagramEdge struct {
	From  string `yaml:"from" json:"from"`
	To    string `yaml:"to" json:"to"`
	Label string `yaml:"label" json:"label,omitempty"`
}

type Environment struct {
	Runtime  string `yaml:"runtime"`
	Topology string `yaml:"topology"`
}

// Traffic uses one profile at a time. Fields that do not belong to the selected
// profile are rejected during validation.
type Traffic struct {
	Profile      string        `yaml:"profile"`
	RPS          float64       `yaml:"rps"`
	Duration     time.Duration `yaml:"duration"`
	FromRPS      float64       `yaml:"from_rps"`
	ToRPS        float64       `yaml:"to_rps"`
	RampDuration time.Duration `yaml:"ramp_duration"`
	HoldDuration time.Duration `yaml:"hold_duration"`
	BaselineRPS  float64       `yaml:"baseline_rps"`
	PeakRPS      float64       `yaml:"peak_rps"`
	Before       time.Duration `yaml:"before_duration"`
	Spike        time.Duration `yaml:"spike_duration"`
	After        time.Duration `yaml:"after_duration"`
}

type Fault struct {
	Target string      `yaml:"target"`
	Type   string      `yaml:"type"`
	Config FaultConfig `yaml:"config"`
}

// FaultConfig holds the bounded parameters supported by the installed fault
// concepts. Each fault type permits only its relevant parameter.
type FaultConfig struct {
	WorkUnits      int     `yaml:"work_units"`
	Probability    float64 `yaml:"probability"`
	LatencyMS      int     `yaml:"latency_ms"`
	Retries        int     `yaml:"retries"`
	MaxConcurrency int     `yaml:"max_concurrency"`
	MemoryMB       int     `yaml:"memory_mb"`
	FailureEvery   int     `yaml:"failure_every"`
}

type Objectives struct {
	Availability float64 `yaml:"availability"`
	P95LatencyMS int     `yaml:"p95_latency_ms"`
}

type Grading struct {
	Kind              string        `yaml:"kind"`
	Window            time.Duration `yaml:"window"`
	MinimumRequests   int           `yaml:"minimum_requests"`
	MinimumOfferedRPS float64       `yaml:"minimum_offered_rps"`
}

// Load reads and validates the complete installed catalog below root/scenarios.
func Load(root string) ([]Scenario, error) {
	directory := filepath.Join(root, "scenarios")
	entries := make([]string, 0, len(requiredIDs))
	if err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || (filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml") {
			return nil
		}
		entries = append(entries, path)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("read scenarios: %w", err)
	}
	sort.Strings(entries)
	if len(entries) == 0 {
		return nil, fmt.Errorf("no scenario definitions in %s", directory)
	}

	scenarios := make([]Scenario, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, path := range entries {
		definition, err := decode(path)
		if err != nil {
			return nil, err
		}
		if err := definition.validate(); err != nil {
			return nil, fmt.Errorf("validate %s: %w", path, err)
		}
		if seen[definition.ID] {
			return nil, fmt.Errorf("duplicate scenario id %q", definition.ID)
		}
		seen[definition.ID] = true
		scenarios = append(scenarios, definition)
	}
	for id := range requiredIDs {
		if !seen[id] {
			return nil, fmt.Errorf("missing required scenario %q", id)
		}
	}
	return scenarios, nil
}

func decode(path string) (Scenario, error) {
	file, err := os.Open(path)
	if err != nil {
		return Scenario{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.SetStrict(true)
	var definition Scenario
	if err := decoder.Decode(&definition); err != nil {
		return Scenario{}, fmt.Errorf("decode %s: %w", path, err)
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Scenario{}, fmt.Errorf("decode %s: multiple YAML documents are not allowed", path)
		}
		return Scenario{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return definition, nil
}

func (s Scenario) validate() error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %d", SchemaVersion)
	}
	if strings.TrimSpace(s.Goal) == "" || len(s.SuccessCriteria) < 2 {
		return fmt.Errorf("goal and at least two success_criteria are required")
	}
	if !validID.MatchString(s.ID) || !requiredIDs[s.ID] {
		return fmt.Errorf("unsupported scenario id %q", s.ID)
	}
	if strings.TrimSpace(s.Title) == "" || strings.TrimSpace(s.Description) == "" {
		return fmt.Errorf("title and description are required")
	}
	if s.Difficulty != "beginner" && s.Difficulty != "intermediate" && s.Difficulty != "advanced" {
		return fmt.Errorf("unsupported difficulty %q", s.Difficulty)
	}
	if err := validateStrings("learning_objectives", s.LearningObjectives); err != nil {
		return err
	}
	for name, values := range map[string][]string{"prerequisites": s.Prerequisites, "required_tools": s.RequiredTools, "false_hypotheses": s.FalseHypotheses} {
		if err := validateStrings(name, values); err != nil {
			return err
		}
	}
	if s.Environment.Runtime != "docker" || s.Environment.Topology != "api-dependency" {
		return fmt.Errorf("environment must use runtime docker and topology api-dependency")
	}
	if err := s.Traffic.validate(); err != nil {
		return err
	}
	if len(s.Faults) == 0 {
		return fmt.Errorf("at least one fault is required")
	}
	for _, fault := range s.Faults {
		if err := fault.validate(); err != nil {
			return err
		}
	}
	if s.Objectives.Availability <= 0 || s.Objectives.Availability > 1 || s.Objectives.P95LatencyMS <= 0 || s.Objectives.P95LatencyMS > 60000 {
		return fmt.Errorf("objectives must specify availability in (0, 1] and p95_latency_ms in (0, 60000]")
	}
	if s.Grading.Kind != "service_recovery" && s.Grading.Kind != "alert_behavior" && s.Grading.Kind != "burn_rate_alert" && s.Grading.Kind != "file_forensics" {
		return fmt.Errorf("unsupported grading kind %q", s.Grading.Kind)
	}
	if err := validateDuration("grading window", s.Grading.Window); err != nil || s.Grading.MinimumRequests <= 0 || s.Grading.MinimumOfferedRPS <= 0 || s.Grading.MinimumOfferedRPS > maxRate {
		if err != nil {
			return err
		}
		return fmt.Errorf("grading minimum requests and offered rps must be positive and offered rps no more than %d", maxRate)
	}
	if err := validateStrings("hints", s.Hints); err != nil {
		return err
	}
	if len(s.Diagram.Nodes) == 0 || len(s.Diagram.Edges) == 0 {
		return fmt.Errorf("diagram must define nodes and edges")
	}
	return nil
}

func (t Traffic) validate() error {
	switch t.Profile {
	case "constant":
		if t.RPS <= 0 || t.RPS > maxRate || t.FromRPS != 0 || t.ToRPS != 0 || t.BaselineRPS != 0 || t.PeakRPS != 0 || t.RampDuration != 0 || t.HoldDuration != 0 || t.Before != 0 || t.Spike != 0 || t.After != 0 {
			return fmt.Errorf("constant traffic requires only rps and duration")
		}
		return validateDuration("traffic duration", t.Duration)
	case "ramp":
		if t.FromRPS <= 0 || t.FromRPS > maxRate || t.ToRPS <= 0 || t.ToRPS > maxRate || t.RPS != 0 || t.Duration != 0 || t.BaselineRPS != 0 || t.PeakRPS != 0 || t.Before != 0 || t.Spike != 0 || t.After != 0 {
			return fmt.Errorf("ramp traffic requires only from_rps, to_rps, ramp_duration, and hold_duration")
		}
		if err := validateDuration("traffic ramp_duration", t.RampDuration); err != nil {
			return err
		}
		return validateDuration("traffic hold_duration", t.HoldDuration)
	case "spike":
		if t.BaselineRPS <= 0 || t.BaselineRPS > maxRate || t.PeakRPS <= t.BaselineRPS || t.PeakRPS > maxRate || t.RPS != 0 || t.Duration != 0 || t.FromRPS != 0 || t.ToRPS != 0 || t.RampDuration != 0 || t.HoldDuration != 0 {
			return fmt.Errorf("spike traffic requires baseline_rps, higher peak_rps, and before/spike/after durations")
		}
		if err := validateDuration("traffic before_duration", t.Before); err != nil {
			return err
		}
		if err := validateDuration("traffic spike_duration", t.Spike); err != nil {
			return err
		}
		return validateDuration("traffic after_duration", t.After)
	default:
		return fmt.Errorf("unsupported traffic profile %q", t.Profile)
	}
}

func (f Fault) validate() error {
	if f.Target != "api" && f.Target != "dependency" {
		return fmt.Errorf("unsupported fault target %q", f.Target)
	}
	switch f.Type {
	case "cpu_work":
		if f.Target != "api" || f.Config.WorkUnits <= 0 || f.Config.Probability != 0 {
			return fmt.Errorf("cpu_work requires target api and positive config.work_units only")
		}
	case "error_rate":
		if f.Config.WorkUnits != 0 || f.Config.Probability <= 0 || f.Config.Probability > 1 {
			return fmt.Errorf("error_rate requires config.probability in (0, 1] only")
		}
	case "dependency_latency":
		if f.Target != "dependency" || f.Config.LatencyMS <= 0 || f.Config.LatencyMS > 10000 {
			return fmt.Errorf("dependency_latency requires dependency target and latency_ms in (0, 10000]")
		}
	case "connection_exhaustion":
		if f.Target != "api" || f.Config.MaxConcurrency <= 0 || f.Config.MaxConcurrency > 1000 {
			return fmt.Errorf("connection_exhaustion requires api target and max_concurrency in (0, 1000]")
		}
	case "dns_failure":
		if f.Target != "dependency" {
			return fmt.Errorf("dns_failure requires dependency target")
		}
	case "retry_storm":
		if f.Target != "api" || f.Config.Retries <= 0 || f.Config.Retries > 10 {
			return fmt.Errorf("retry_storm requires api target and retries in (0, 10]")
		}
	case "memory_leak":
		if f.Target != "api" || f.Config.MemoryMB <= 0 || f.Config.MemoryMB > 1024 {
			return fmt.Errorf("memory_leak requires api target and memory_mb in (0, 1024]")
		}
	case "autoscaler_oscillation":
		if f.Target != "api" {
			return fmt.Errorf("autoscaler_oscillation requires api target")
		}
	case "file_handle":
		if f.Target != "api" || f.Config != (FaultConfig{}) {
			return fmt.Errorf("file_handle requires api target and empty config")
		}
	case "network_policy":
		if f.Target != "dependency" || f.Config != (FaultConfig{}) {
			return fmt.Errorf("network_policy requires dependency target and empty config")
		}
	default:
		return fmt.Errorf("unsupported fault type %q", f.Type)
	}
	return nil
}

func validateDuration(name string, duration time.Duration) error {
	if duration <= 0 || duration > maxDuration {
		return fmt.Errorf("%s must be in (0, %s]", name, maxDuration)
	}
	return nil
}

func validateStrings(name string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s must not be empty", name)
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s must not contain empty values", name)
		}
	}
	return nil
}
