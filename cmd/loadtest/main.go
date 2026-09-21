package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	target := flag.String("target", "http://localhost:8081", "target node")
	total := flag.Int("n", 5000, "requests")
	workers := flag.Int("c", 50, "concurrency")
	writes := flag.Int("writes", 20, "write percentage")
	flag.Parse()
	jobs := make(chan int)
	lat := make(chan time.Duration, *total)
	var ok atomic.Uint64
	client := &http.Client{Timeout: 4 * time.Second}
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				key := fmt.Sprintf("key-%d", i%1000)
				method := http.MethodGet
				var body io.Reader
				if i%100 < *writes {
					method = http.MethodPut
					body = bytes.NewBufferString(fmt.Sprintf(`{"value":"value-%d"}`, i))
				}
				t := time.Now()
				req, _ := http.NewRequest(method, *target+"/kv/"+key, body)
				if body != nil {
					req.Header.Set("Content-Type", "application/json")
				}
				resp, err := client.Do(req)
				lat <- time.Since(t)
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if resp.StatusCode < 500 {
						ok.Add(1)
					}
				}
			}
		}()
	}
	go func() {
		for i := 0; i < *total; i++ {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(lat)
	}()
	vals := make([]time.Duration, 0, *total)
	for d := range lat {
		vals = append(vals, d)
	}
	elapsed := time.Since(start)
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	pct := func(p float64) time.Duration {
		if len(vals) == 0 {
			return 0
		}
		idx := int(float64(len(vals)-1) * p)
		return vals[idx]
	}
	fmt.Printf("requests=%d successful=%d concurrency=%d elapsed=%s throughput=%.1f req/s\n", *total, ok.Load(), *workers, elapsed, float64(*total)/elapsed.Seconds())
	fmt.Printf("p50=%s p95=%s p99=%s\n", pct(.50), pct(.95), pct(.99))
}
