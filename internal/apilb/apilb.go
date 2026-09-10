package apilb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

const (
	defaultBackend1 = "http://api-1:8080"
	defaultBackend2 = "http://api-2:8080"
	defaultTimeout  = 2 * time.Second
)

type Config struct {
	Backend1URL string
	Backend2URL string
	Timeout     time.Duration
}

func ConfigFromEnv() Config {
	return Config{
		Backend1URL: env("BACKEND_1_URL", defaultBackend1),
		Backend2URL: env("BACKEND_2_URL", defaultBackend2),
		Timeout:     defaultTimeout,
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

type Handler struct {
	http.Handler
	transport *http.Transport
}

type loadBalancer struct {
	backends [2]*url.URL
	client   *http.Client
	active   atomic.Int32
	next     atomic.Uint64
}

func New(c Config) (*Handler, error) {
	if c.Timeout <= 0 {
		c.Timeout = defaultTimeout
	}
	backends := [2]*url.URL{}
	for i, raw := range []string{c.Backend1URL, c.Backend2URL} {
		backend, err := parseBackend(raw)
		if err != nil {
			return nil, err
		}
		backends[i] = backend
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = c.Timeout
	lb := &loadBalancer{
		backends: backends,
		client: &http.Client{
			Transport: transport,
			Timeout:   c.Timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
	lb.active.Store(1)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", lb.healthz)
	mux.HandleFunc("/admin/state", lb.state)
	mux.HandleFunc("/", lb.proxy)
	return &Handler{Handler: mux, transport: transport}, nil
}

func (h *Handler) Close() {
	if h != nil && h.transport != nil {
		h.transport.CloseIdleConnections()
	}
}

func parseBackend(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("invalid backend URL %q", raw)
	}
	u.Path = ""
	u.RawPath = ""
	return u, nil
}

func (lb *loadBalancer) healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n")
}

func (lb *loadBalancer) state(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		lb.writeState(w)
	case http.MethodPut:
		lb.updateState(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (lb *loadBalancer) writeState(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		ActiveBackends int32 `json:"active_backends"`
	}{ActiveBackends: lb.active.Load()})
}

func (lb *loadBalancer) updateState(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	var input struct {
		ActiveBackends *int32 `json:"active_backends"`
	}
	if err := decoder.Decode(&input); err != nil || input.ActiveBackends == nil || *input.ActiveBackends != 1 && *input.ActiveBackends != 2 {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	lb.active.Store(*input.ActiveBackends)
	lb.writeState(w)
}

func (lb *loadBalancer) proxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/" {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	count := int(lb.active.Load())
	index := int(lb.next.Add(1)-1) % count
	target := *lb.backends[index]
	target.Path = "/"
	target.RawPath = ""
	target.RawQuery = r.URL.RawQuery

	ctx, cancel := context.WithTimeout(r.Context(), lb.client.Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
		return
	}
	request.Host = target.Host
	if runID := r.Header.Get("X-SRE-Run-ID"); runID != "" {
		request.Header.Set("X-SRE-Run-ID", runID)
	}
	response, err := lb.client.Do(request)
	if err != nil || response.StatusCode >= 300 && response.StatusCode < 400 {
		if response != nil {
			response.Body.Close()
		}
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
		return
	}
	defer response.Body.Close()
	copyHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		if strings.EqualFold(key, "Connection") || strings.EqualFold(key, "Keep-Alive") || strings.EqualFold(key, "Proxy-Authenticate") || strings.EqualFold(key, "Proxy-Authorization") || strings.EqualFold(key, "TE") || strings.EqualFold(key, "Trailer") || strings.EqualFold(key, "Transfer-Encoding") || strings.EqualFold(key, "Upgrade") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
