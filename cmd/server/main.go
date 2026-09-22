package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/PnikFlod6021/go-distributed-kv/internal/cluster"
	"github.com/PnikFlod6021/go-distributed-kv/internal/store"
)

type metrics struct {
	requests     atomic.Uint64
	readMiss     atomic.Uint64
	replFail     atomic.Uint64
	latencyMu    sync.Mutex
	latencyTotal time.Duration
}

type server struct {
	store   *store.Store
	cluster *cluster.Cluster
	node    cluster.Node
	m       metrics
}
type kvRequest struct {
	Value string `json:"value"`
}

func main() {
	nodeID := flag.String("node", envOr("NODE_ID", "node-1"), "node id")
	selfURL := flag.String("self-url", envOr("SELF_URL", "http://localhost:8080"), "URL peers use to reach this node")
	addr := flag.String("addr", envOr("ADDR", ":8080"), "listen address")
	nodesRaw := flag.String("nodes", envOr("NODES", ""), "comma separated id=url nodes")
	wal := flag.String("wal", envOr("WAL_FILE", "./data/store.wal"), "WAL path")
	repl := flag.Int("replicas", envInt("REPLICATION_FACTOR", 2), "replication factor")
	flag.Parse()

	st := store.New(*wal)
	if err := st.Load(); err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	self := cluster.Node{ID: *nodeID, URL: strings.TrimRight(*selfURL, "/")}
	cl := cluster.New(self, parseNodes(*nodesRaw), *repl, 1500*time.Millisecond)
	s := &server{store: st, cluster: cl, node: self}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.root)
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/metrics", s.metrics)
	mux.HandleFunc("/cluster", s.clusterInfo)
	mux.HandleFunc("/kv/", s.kv)
	mux.HandleFunc("/internal/apply/", s.apply)
	mux.HandleFunc("/internal/value/", s.internalValue)

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		s.m.requests.Add(1)
		mux.ServeHTTP(w, r)
		d := time.Since(start)
		s.m.latencyMu.Lock()
		s.m.latencyTotal += d
		s.m.latencyMu.Unlock()
		log.Printf("node=%s %s %s %s", s.node.ID, r.Method, r.URL.Path, d)
	})
	srv := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Printf("node=%s addr=%s self=%s replicas=%d", self.ID, *addr, self.URL, *repl)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func (s *server) root(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{"service": "go-distributed-kv", "node": s.node, "owners_example": s.cluster.Owners("example")})
}
func (s *server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok", "node": s.node.ID})
}
func (s *server) clusterInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	writeJSON(w, 200, map[string]any{"self": s.node, "leader": s.cluster.Leader(ctx), "nodes": s.cluster.Nodes()})
}

func (s *server) kv(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/kv/")
	if key == "" {
		http.Error(w, "key required", 400)
		return
	}
	switch r.Method {
	case http.MethodGet:
		v, node, ok, err := s.cluster.FetchFromOwners(r.Context(), key)
		if err != nil {
			http.Error(w, "replica read unavailable", http.StatusServiceUnavailable)
			return
		}
		if !ok {
			s.m.readMiss.Add(1)
			http.Error(w, "key not found", 404)
			return
		}
		writeJSON(w, 200, map[string]string{"key": key, "value": v, "served_by": node})
	case http.MethodPut, http.MethodDelete:
		var value string
		if r.Method == http.MethodPut {
			var req kvRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", 400)
				return
			}
			value = req.Value
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		leader := s.cluster.Leader(ctx)
		if leader.ID != s.node.ID && r.Header.Get("X-KV-Forwarded") != "1" {
			resp, err := s.cluster.ForwardWrite(ctx, leader, r.Method, key, value)
			if err != nil {
				http.Error(w, "leader unavailable", 503)
				return
			}
			defer resp.Body.Close()
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
			return
		}
		owners := s.cluster.Owners(key)
		ack := s.cluster.ApplyToOwners(ctx, r.Method, key, value)
		quorum := len(owners)/2 + 1
		if ack < quorum {
			s.m.replFail.Add(1)
			http.Error(w, fmt.Sprintf("write quorum failed: %d/%d acknowledgements", ack, len(owners)), 503)
			return
		}
		writeJSON(w, 200, map[string]any{"key": key, "leader": s.node.ID, "replicas_acknowledged": ack, "replica_target": len(owners), "owners": owners})
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(w, "method not allowed", 405)
	}
}

func (s *server) apply(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/internal/apply/")
	if key == "" {
		http.Error(w, "key required", 400)
		return
	}
	switch r.Method {
	case http.MethodPut:
		var req kvRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "bad JSON", 400)
			return
		}
		if s.store.Set(key, req.Value) != nil {
			http.Error(w, "wal failure", 500)
			return
		}
		w.WriteHeader(204)
	case http.MethodDelete:
		if s.store.Delete(key) != nil {
			http.Error(w, "wal failure", 500)
			return
		}
		w.WriteHeader(204)
	default:
		http.Error(w, "method not allowed", 405)
	}
}
func (s *server) internalValue(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/internal/value/")
	v, ok := s.store.Get(key)
	if !ok {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, 200, map[string]string{"value": v})
}
func (s *server) metrics(w http.ResponseWriter, r *http.Request) {
	s.m.latencyMu.Lock()
	total := s.m.latencyTotal
	s.m.latencyMu.Unlock()
	req := s.m.requests.Load()
	avg := float64(0)
	if req > 0 {
		avg = total.Seconds() / float64(req)
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "kv_requests_total %d\nkv_read_misses_total %d\nkv_replication_failures_total %d\nkv_keys %d\nkv_request_latency_seconds_avg %.6f\n", req, s.m.readMiss.Load(), s.m.replFail.Load(), s.store.Len(), avg)
}

func parseNodes(raw string) []cluster.Node {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := []cluster.Node{}
	for _, p := range parts {
		x := strings.SplitN(strings.TrimSpace(p), "=", 2)
		if len(x) == 2 {
			out = append(out, cluster.Node{ID: x[0], URL: strings.TrimRight(x[1], "/")})
		}
	}
	return out
}
func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envInt(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, e := strconv.Atoi(v); e == nil {
			return n
		}
	}
	return d
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
