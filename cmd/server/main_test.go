package main

import (
	"net/http"
	"net/http/httptest"
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
