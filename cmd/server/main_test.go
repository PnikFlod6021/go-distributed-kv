package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PnikFlod6021/go-distributed-kv/internal/cluster"
)

func TestReadStatusAndMissCount(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		peerStatus, wantStatus int
		wantMisses             uint64
	}{
		{"found", 200, 200, 0},
		{"missing", 404, 404, 1},
		{"unavailable", 503, 503, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.peerStatus)
				_, _ = w.Write([]byte(`{"value":"saved"}`))
			}))
			defer peer.Close()
			node := cluster.Node{ID: "one", URL: peer.URL}
			s := &server{cluster: cluster.New(node, nil, 1, time.Second)}
			response := httptest.NewRecorder()
			s.kv(response, httptest.NewRequest(http.MethodGet, "/kv/key", nil))
			if response.Code != tc.wantStatus || s.m.readMiss.Load() != tc.wantMisses {
				t.Fatalf("status=%d misses=%d; want %d and %d", response.Code, s.m.readMiss.Load(), tc.wantStatus, tc.wantMisses)
			}
		})
	}
}

func TestForwardedWriteResponse(t *testing.T) {
	for _, tc := range []struct {
		name, method, contentType, body string
		status                          int
	}{
		{"put", http.MethodPut, "application/json", `{"replicas_acknowledged":2}`, 200},
		{"delete", http.MethodDelete, "application/json", `{"replicas_acknowledged":2}`, 200},
		{"quorum failure", http.MethodPut, "text/plain; charset=utf-8", "write quorum failed\n", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/healthz" {
					w.WriteHeader(http.StatusOK)
					return
				}
				if r.URL.Path != "/kv/key" || r.Method != tc.method || r.Header.Get("X-KV-Forwarded") != "1" {
					t.Errorf("unexpected forwarded request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer leader.Close()
			self := cluster.Node{ID: "node-2", URL: "http://localhost:1"}
			s := &server{node: self, cluster: cluster.New(self, []cluster.Node{{ID: "node-1", URL: leader.URL}}, 2, time.Second)}
			response := httptest.NewRecorder()
			s.kv(response, httptest.NewRequest(tc.method, "/kv/key", strings.NewReader(`{"value":"saved"}`)))
			if response.Code != tc.status || response.Header().Get("Content-Type") != tc.contentType || response.Body.String() != tc.body {
				t.Fatalf("forwarded response changed: status=%d type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
			}
		})
	}
}

func TestInternalMethods(t *testing.T) {
	s := &server{}
	for _, tc := range []struct {
		path, method, allow string
		handler             http.HandlerFunc
	}{
		{"/internal/value/key", http.MethodPost, "GET", s.internalValue},
		{"/internal/value/key", http.MethodDelete, "GET", s.internalValue},
		{"/internal/apply/key", http.MethodGet, "PUT, DELETE", s.apply},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			tc.handler(response, httptest.NewRequest(tc.method, tc.path, nil))
			if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != tc.allow {
				t.Fatalf("status=%d Allow=%q", response.Code, response.Header().Get("Allow"))
			}
		})
	}
}
