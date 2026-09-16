package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sre-lab/internal/progress"
)

const DefaultOrigins = "http://localhost:8080,http://127.0.0.1:8080"

func OriginsFromEnv() []string {
	return strings.Split(env("ALLOWED_ORIGINS", DefaultOrigins), ",")
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// ParseOrigins accepts only serialized HTTP origins, not URL paths or wildcards.
func ParseOrigins(origins []string) (map[string]bool, map[string]bool, error) {
	allowed, hosts := map[string]bool{}, map[string]bool{}
	for _, origin := range origins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(u.Host, "*\\") || origin != u.Scheme+"://"+u.Host {
			return nil, nil, fmt.Errorf("invalid allowed origin %q", origin)
		}
		allowed[origin], hosts[u.Host] = true, true
	}
	if len(allowed) == 0 {
		return nil, nil, fmt.Errorf("at least one allowed origin is required")
	}
	return allowed, hosts, nil
}

type Config struct {
	Origins              []string
	StaticDir            string
	ToolboxURL           string
	PrometheusURL        string
	APIURL               string
	DependencyURL        string
	DependencyControlURL string
	HistoryPath          string
	ProgressPath         string
	ScenarioRoot         string
	TrafficURL           string
	LBURL                string
	API1URL              string
	API2URL              string
	PolicyControlURL     string
}

func ConfigFromEnv() Config {
	return Config{Origins: OriginsFromEnv(), StaticDir: env("STATIC_DIR", "/app/frontend"), ToolboxURL: env("TOOLBOX_URL", "http://toolbox:8080"), PrometheusURL: env("PROMETHEUS_URL", "http://prometheus:9090"), APIURL: env("API_URL", "http://api-lb:8080"), DependencyURL: env("DEPENDENCY_URL", "http://dependency:8080"), DependencyControlURL: env("DEPENDENCY_CONTROL_URL", "http://dependency:8080"), PolicyControlURL: env("POLICY_CONTROL_URL", "http://policy:8080"), ProgressPath: env("PROGRESS_PATH", "/data/progress.json"), HistoryPath: env("HISTORY_PATH", "/data/runs.json"), ScenarioRoot: env("SCENARIO_ROOT", "/app"), TrafficURL: env("TRAFFIC_URL", "http://traffic:8080"), LBURL: env("LB_URL", "http://api-lb:8080"), API1URL: env("API1_URL", "http://api-1:8080"), API2URL: env("API2_URL", "http://api-2:8080")}
}

func upstream(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("invalid upstream %q: expected an HTTP origin", raw)
	}
	return u, nil
}

type Handler struct {
	http.Handler
	close   func()
	runtime *runtimeManager
}

func (h *Handler) Close() { h.close() }

func New(c Config) (*Handler, error) {
	if c.ProgressPath == "" {
		c.ProgressPath = filepath.Join(c.StaticDir, ".progress.json")
	}
	if c.HistoryPath == "" {
		c.HistoryPath = filepath.Join(c.StaticDir, ".runs.json")
	}
	origins, hosts, err := ParseOrigins(c.Origins)
	if err != nil {
		return nil, err
	}
	store, err := progress.Open(c.ProgressPath)
	if err != nil {
		return nil, err
	}
	var runtime *runtimeManager
	if _, statErr := os.Stat(filepath.Join(c.ScenarioRoot, "scenarios")); statErr == nil {
		runtime, err = newRuntime(runtimeConfig{Root: c.ScenarioRoot, TrafficURL: c.TrafficURL, PromURL: c.PrometheusURL, LBURL: c.LBURL, API1URL: c.API1URL, API2URL: c.API2URL, DependencyURL: c.DependencyControlURL, PolicyURL: c.PolicyControlURL, HistoryPath: c.HistoryPath})
		if err != nil {
			return nil, err
		}
	}
	targets := make([]*url.URL, 4)
	for i, raw := range []string{c.APIURL, c.DependencyURL, c.ToolboxURL, c.PrometheusURL} {
		targets[i], err = upstream(raw)
		if err != nil {
			return nil, err
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 5 * time.Second
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	proxy := func(target *url.URL, terminal bool) *httputil.ReverseProxy {
		return &httputil.ReverseProxy{
			Transport: transport,
			Rewrite: func(p *httputil.ProxyRequest) {
				p.SetURL(target)
				p.Out.Host = target.Host
				p.Out.Header.Del("Forwarded")
				p.Out.Header.Del("X-Forwarded-Host")
				p.Out.Header.Del("X-Forwarded-Proto")
				p.Out.Header.Del("X-Forwarded-For")
				p.Out.Header.Del("Authorization")
				p.Out.Header.Del("Cookie")
				if terminal {
					p.Out.URL.Path = "/terminal"
					p.Out.URL.RawQuery = ""
				} else {
					p.Out.URL.Path = strings.TrimPrefix(p.In.URL.Path, "/prometheus")
				}
				p.Out.URL.RawPath = ""
			},
			ModifyResponse: func(r *http.Response) error {
				r.Header.Del("Set-Cookie")
				if r.StatusCode >= 300 && r.StatusCode < 400 && r.StatusCode != http.StatusNotModified {
					location, err := r.Location()
					if terminal || err != nil || location.Scheme != target.Scheme || location.Host != target.Host || location.User != nil {
						return fmt.Errorf("upstream redirect refused")
					}
					p := strings.TrimPrefix(location.Path, "/prometheus")
					if !safePath(p) || !prometheusPath(p) {
						return fmt.Errorf("unsafe redirect path")
					}
					location.Scheme, location.Host, location.RawPath = "", "", ""
					location.Path = "/prometheus" + p
					r.Header.Set("Location", location.String())
				}
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				http.Error(w, "upstream unavailable", http.StatusBadGateway)
			},
		}
	}
	terminalProxy, prometheusProxy := proxy(targets[2], true), proxy(targets[3], false)
	root, err := os.OpenRoot(c.StaticDir)
	// The API remains available if frontend assets have not been built yet.
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	shutdown, stop := context.WithCancel(context.Background())
	var files http.Handler
	if root != nil {
		files = http.FileServerFS(root.FS())
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'self'; frame-ancestors 'self'; object-src 'none'; base-uri 'self'; form-action 'self'")
		// Prometheus bootstraps its bundled UI with an inline configuration script.
		// Keep this exception off the SRE Lab frontend and terminal endpoints.
		if strings.HasPrefix(r.URL.Path, "/prometheus/") {
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'self'; object-src 'none'; base-uri 'self'; form-action 'self'")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if !hosts[r.Host] {
			http.Error(w, "host forbidden", http.StatusForbidden)
			return
		}
		isProgressWrite := r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/progress/")
		isRuntimeMutation := runtime != nil && (strings.HasSuffix(r.URL.Path, "/start") || strings.HasSuffix(r.URL.Path, "/reset") || strings.HasSuffix(r.URL.Path, "/hint") || strings.HasSuffix(r.URL.Path, "/capacity") || strings.HasSuffix(r.URL.Path, "/phase") || strings.HasSuffix(r.URL.Path, "/intervention") || strings.HasSuffix(r.URL.Path, "/alert") || strings.HasSuffix(r.URL.Path, "/check") || strings.HasSuffix(r.URL.Path, "/file-evidence") || strings.HasSuffix(r.URL.Path, "/diagnosis")) && (r.Method == http.MethodPost || r.Method == http.MethodPut)
		if r.Method != http.MethodGet && !isProgressWrite && !isRuntimeMutation && !(r.Method == http.MethodHead && !strings.HasPrefix(r.URL.Path, "/api") && !strings.HasPrefix(r.URL.Path, "/prometheus") && r.URL.Path != "/terminal" && r.URL.Path != "/healthz") {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !safePath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/healthz":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			io.WriteString(w, "ok\n")
		case "/api/status":
			type component struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			}
			result := struct {
				Status     string      `json:"status"`
				Phase      int         `json:"phase"`
				Components []component `json:"components"`
			}{Status: "healthy", Phase: 1, Components: make([]component, 4)}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			var wg sync.WaitGroup
			for i, name := range []string{"api", "dependency", "toolbox", "prometheus"} {
				wg.Add(1)
				go func(i int, name string) {
					defer wg.Done()
					result.Components[i] = component{name, "unavailable"}
					u := *targets[i]
					u.Path = "/healthz"
					if name == "prometheus" {
						u.Path = "/-/ready"
					}
					req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
					resp, err := client.Do(req)
					if err == nil {
						defer resp.Body.Close()
						if resp.StatusCode == http.StatusOK {
							result.Components[i].Status = "healthy"
						}
					}
				}(i, name)
			}
			wg.Wait()
			for _, c := range result.Components {
				if c.Status != "healthy" {
					result.Status = "degraded"
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			json.NewEncoder(w).Encode(result)
		default:
			if runtime != nil && (strings.HasPrefix(r.URL.Path, "/api/scenarios") || strings.HasPrefix(r.URL.Path, "/api/run") || r.URL.Path == "/api/runs") {
				if r.Method != http.MethodGet && (len(r.Header.Values("Origin")) != 1 || !origins[r.Header.Get("Origin")]) {
					http.Error(w, "origin forbidden", http.StatusForbidden)
					return
				}
				handleRuntime(w, r, runtime)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/progress/") {
				handleProgress(w, r, origins, store)
				return
			}
			if r.URL.Path == "/terminal" {
				if len(r.Header.Values("Origin")) != 1 || !origins[r.Header.Get("Origin")] {
					http.Error(w, "origin forbidden", http.StatusForbidden)
					return
				}
				if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
					http.Error(w, "websocket required", http.StatusBadRequest)
					return
				}
				ctx, cancel := context.WithCancel(r.Context())
				unregister := context.AfterFunc(shutdown, cancel)
				defer unregister()
				defer cancel()
				terminalProxy.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api") || strings.HasPrefix(r.URL.Path, "/terminal/") {
				http.NotFound(w, r)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/prometheus") {
				if !prometheusPath(strings.TrimPrefix(r.URL.Path, "/prometheus")) {
					http.NotFound(w, r)
					return
				}
				ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
				defer cancel()
				prometheusProxy.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			if root == nil {
				http.NotFound(w, r)
				return
			}
			name := strings.TrimPrefix(r.URL.Path, "/")
			if name == "" {
				name = "index.html"
			}
			info, err := root.Stat(name)
			if err == nil && info.Mode().IsRegular() {
				files.ServeHTTP(w, r)
				return
			}
			if err == nil || !os.IsNotExist(err) || path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			index, err := root.Open("index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer index.Close()
			stat, err := index.Stat()
			if err != nil || !stat.Mode().IsRegular() {
				http.NotFound(w, r)
				return
			}
			http.ServeContent(w, r, "index.html", stat.ModTime(), index)
		}
	})
	return &Handler{Handler: handler, runtime: runtime, close: func() {
		stop()
		transport.CloseIdleConnections()
		if root != nil {
			root.Close()
		}
	}}, nil
}

func handleProgress(w http.ResponseWriter, r *http.Request, origins map[string]bool, store *progress.Store) {
	id := strings.TrimPrefix(r.URL.Path, "/api/progress/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		record, ok := store.Get(id)
		if !ok {
			record = progress.Record{CompletedSteps: []int{}}
		}
		json.NewEncoder(w).Encode(record)
	case http.MethodPut:
		if len(r.Header.Values("Origin")) != 1 || !origins[r.Header.Get("Origin")] || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "origin or content type forbidden", http.StatusForbidden)
			return
		}
		defer r.Body.Close()
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		var input struct {
			CompletedSteps []int  `json:"completedSteps"`
			Notes          string `json:"notes"`
		}
		if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid progress", http.StatusBadRequest)
			return
		}
		record, err := store.Put(id, input.CompletedSteps, input.Notes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(record)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func safePath(p string) bool {
	if strings.ContainsAny(p, "\\\x00") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || part == "." {
			return false
		}
	}
	return !strings.Contains(p, "//")
}

func prometheusPath(p string) bool {
	switch p {
	case "/", "/query", "/graph", "/alerts", "/targets", "/service-discovery", "/status", "/rules", "/favicon.ico", "/manifest.json", "/api/v1/query", "/api/v1/query_range", "/api/v1/labels", "/api/v1/series", "/api/v1/metadata", "/api/v1/targets", "/api/v1/targets/metadata", "/api/v1/rules", "/api/v1/alerts", "/api/v1/status/buildinfo", "/api/v1/status/runtimeinfo", "/api/v1/status/flags":
		return true
	}
	if strings.HasPrefix(p, "/assets/") || strings.HasPrefix(p, "/static/") {
		return true
	}
	if strings.HasPrefix(p, "/api/v1/label/") && strings.HasSuffix(p, "/values") {
		label := strings.TrimSuffix(strings.TrimPrefix(p, "/api/v1/label/"), "/values")
		return label != "" && !strings.Contains(label, "/")
	}
	return false
}
