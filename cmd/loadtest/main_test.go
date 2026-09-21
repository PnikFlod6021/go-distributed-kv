package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, tc := range []struct {
		name, target           string
		total, workers, writes int
	}{
		{"zero requests", "http://localhost:8080", 0, 1, 20},
		{"negative requests", "http://localhost:8080", -1, 1, 20},
		{"zero workers", "http://localhost:8080", 1, 0, 20},
		{"negative workers", "http://localhost:8080", 1, -1, 20},
		{"negative writes", "http://localhost:8080", 1, 1, -1},
		{"excess writes", "http://localhost:8080", 1, 1, 101},
		{"relative target", "localhost:8080", 1, 1, 20},
		{"wrong scheme", "ftp://localhost", 1, 1, 20},
		{"missing host", "http:///", 1, 1, 20},
		{"invalid escape", "http://localhost/%zz", 1, 1, 20},
		{"query", "http://localhost/?a=b", 1, 1, 20},
		{"fragment", "http://localhost/#part", 1, 1, 20},
		{"credentials", "http://user:pass@localhost", 1, 1, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validate(tc.target, tc.total, tc.workers, tc.writes); err == nil {
				t.Fatal("expected invalid arguments to be rejected")
			}
		})
	}
	for _, writes := range []int{0, 100} {
		if err := validate("https://localhost:8080/prefix/", 10, 2, writes); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunLoadResponseOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		truncated bool
		want      uint64
	}{
		{"success", 200, false, 8},
		{"missing key", 404, false, 0},
		{"unavailable", 503, false, 0},
		{"incomplete body", 200, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.truncated {
					w.Header().Set("Content-Length", "100")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("value"))
			}))
			defer srv.Close()
			got := runLoad(srv.URL, 8, 3, 0)
			if got.successful != tc.want || len(got.latencies) != 8 {
				t.Fatalf("successful=%d samples=%d; want %d and 8", got.successful, len(got.latencies), tc.want)
			}
		})
	}
}

func TestRunLoadWriteMix(t *testing.T) {
	var writes, reads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			writes.Add(1)
		case http.MethodGet:
			reads.Add(1)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	got := runLoad(srv.URL, 100, 4, 20)
	if writes.Load() != 20 || reads.Load() != 80 || got.successful != 100 {
		t.Fatalf("writes=%d reads=%d successful=%d", writes.Load(), reads.Load(), got.successful)
	}
}

func TestRunLoadUnavailableTarget(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	got := runLoad(srv.URL, 2, 1, 0)
	if got.successful != 0 || len(got.latencies) != 2 {
		t.Fatalf("successful=%d samples=%d; want 0 and 2", got.successful, len(got.latencies))
	}
}
