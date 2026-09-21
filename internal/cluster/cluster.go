package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type Node struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}
type point struct {
	hash uint32
	node Node
}

type Cluster struct {
	self     Node
	nodes    []Node
	ring     []point
	replicas int
	client   *http.Client
}

func New(self Node, nodes []Node, replicas int, timeout time.Duration) *Cluster {
	if replicas < 1 {
		replicas = 1
	}
	dedup := map[string]Node{}
	for _, n := range append(nodes, self) {
		if n.ID != "" && n.URL != "" {
			n.URL = strings.TrimRight(n.URL, "/")
			dedup[n.ID] = n
		}
	}
	all := make([]Node, 0, len(dedup))
	for _, n := range dedup {
		all = append(all, n)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	c := &Cluster{self: self, nodes: all, replicas: replicas, client: &http.Client{Timeout: timeout}}
	for _, n := range all {
		for v := 0; v < 64; v++ {
			c.ring = append(c.ring, point{hash: hash(fmt.Sprintf("%s#%d", n.ID, v)), node: n})
		}
	}
	sort.Slice(c.ring, func(i, j int) bool { return c.ring[i].hash < c.ring[j].hash })
	return c
}

func (c *Cluster) Nodes() []Node { out := make([]Node, len(c.nodes)); copy(out, c.nodes); return out }

func (c *Cluster) Healthy(ctx context.Context, n Node) bool {
	if n.ID == c.self.ID {
		return true
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, n.URL+"/healthz", nil)
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Leader returns the lexicographically smallest healthy node.
// Independent health checks can select different leaders during a partition.
func (c *Cluster) Leader(ctx context.Context) Node {
	for _, n := range c.nodes {
		if c.Healthy(ctx, n) {
			return n
		}
	}
	return c.self
}

func (c *Cluster) Owners(key string) []Node {
	if len(c.ring) == 0 {
		return nil
	}
	target := hash(key)
	i := sort.Search(len(c.ring), func(i int) bool { return c.ring[i].hash >= target })
	if i == len(c.ring) {
		i = 0
	}
	need := c.replicas
	if need > len(c.nodes) {
		need = len(c.nodes)
	}
	out := make([]Node, 0, need)
	seen := map[string]bool{}
	for step := 0; len(out) < need && step < len(c.ring)*2; step++ {
		n := c.ring[(i+step)%len(c.ring)].node
		if !seen[n.ID] {
			seen[n.ID] = true
			out = append(out, n)
		}
	}
	return out
}

func (c *Cluster) ApplyToOwners(ctx context.Context, method, key, value string) int {
	owners := c.Owners(key)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for _, n := range owners {
		n := n
		wg.Add(1)
		go func() {
			defer wg.Done()
			var body io.Reader
			if method == http.MethodPut {
				b, _ := json.Marshal(map[string]string{"value": value})
				body = bytes.NewReader(b)
			}
			endpoint := n.URL + "/internal/apply/" + url.PathEscape(key)
			req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
			if err != nil {
				return
			}
			if method == http.MethodPut {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := c.client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return ok
}

func (c *Cluster) ForwardWrite(ctx context.Context, leader Node, method, key, value string) (*http.Response, error) {
	var body io.Reader
	if method == http.MethodPut {
		b, _ := json.Marshal(map[string]string{"value": value})
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, leader.URL+"/kv/"+url.PathEscape(key), body)
	if err != nil {
		return nil, err
	}
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-KV-Forwarded", "1")
	return c.client.Do(req)
}

func (c *Cluster) FetchFromOwners(ctx context.Context, key string) (string, string, bool) {
	for _, n := range c.Owners(key) {
		endpoint := n.URL + "/internal/value/" + url.PathEscape(key)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		resp, err := c.client.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode == http.StatusOK {
			var x struct {
				Value string `json:"value"`
			}
			err = json.NewDecoder(resp.Body).Decode(&x)
			resp.Body.Close()
			if err == nil {
				return x.Value, n.ID, true
			}
		} else {
			resp.Body.Close()
		}
	}
	return "", "", false
}

func hash(s string) uint32 { h := fnv.New32a(); _, _ = h.Write([]byte(s)); return h.Sum32() }
