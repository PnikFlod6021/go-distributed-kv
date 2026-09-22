package cluster

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchFromOwners(t *testing.T) {
	type reply struct {
		status int
		body   string
	}
	found := reply{http.StatusOK, `{"value":"saved"}`}
	missing := reply{http.StatusNotFound, "not found"}
	offline := reply{}
	for _, tc := range []struct {
		name                       string
		first, second              reply
		wantFound, wantUnavailable bool
		wantValue                  string
	}{
		{"both missing", missing, missing, false, false, ""},
		{"both offline", offline, offline, false, true, ""},
		{"offline then missing", offline, missing, false, true, ""},
		{"missing then offline", missing, offline, false, true, ""},
		{"server error", reply{500, "failed"}, missing, false, true, ""},
		{"broken JSON", reply{200, "{"}, missing, false, true, ""},
		{"missing value field", reply{200, `{}`}, missing, false, true, ""},
		{"fallback after offline owner", offline, found, true, false, "saved"},
		{"fallback after bad response", reply{200, "{"}, found, true, false, "saved"},
		{"fallback after missing key", missing, found, true, false, "saved"},
		{"empty stored value", reply{200, `{"value":""}`}, missing, true, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responses := map[string]*reply{"one": {}, "two": {}}
			servers := make(map[string]*httptest.Server)
			var nodes []Node
			for _, id := range []string{"one", "two"} {
				response := responses[id]
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(response.status)
					_, _ = w.Write([]byte(response.body))
				}))
				t.Cleanup(srv.Close)
				servers[id] = srv
				nodes = append(nodes, Node{ID: id, URL: srv.URL})
			}
			c := New(nodes[0], nodes, 2, time.Second)
			owners := c.Owners("key")
			for i, response := range []reply{tc.first, tc.second} {
				id := owners[i].ID
				*responses[id] = response
				if response.status == 0 {
					servers[id].Close()
				}
			}
			value, owner, ok, err := c.FetchFromOwners(context.Background(), "key")
			if ok != tc.wantFound || value != tc.wantValue || errors.Is(err, ErrReadUnavailable) != tc.wantUnavailable {
				t.Fatalf("got value=%q found=%v err=%v", value, ok, err)
			}
			if ok && owner == "" {
				t.Fatal("successful read did not identify its owner")
			}
		})
	}
}

func TestFetchWithoutOwners(t *testing.T) {
	c := New(Node{}, nil, 2, time.Second)
	_, _, found, err := c.FetchFromOwners(context.Background(), "key")
	if found || !errors.Is(err, ErrReadUnavailable) {
		t.Fatalf("found=%v err=%v; want unavailable", found, err)
	}
}
