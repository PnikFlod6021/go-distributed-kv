package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
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
	if err := validate(*target, *total, *workers, *writes); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	result := runLoad(strings.TrimRight(*target, "/"), *total, *workers, *writes)
	fmt.Printf("requests=%d successful=%d failed=%d concurrency=%d elapsed=%s throughput=%.1f req/s\n", *total, result.successful, *total-int(result.successful), *workers, result.elapsed, float64(*total)/result.elapsed.Seconds())
	fmt.Printf("p50=%s p95=%s p99=%s\n", result.percentile(.50), result.percentile(.95), result.percentile(.99))
}

func validate(target string, total, workers, writes int) error {
	if total < 1 {
		return fmt.Errorf("n must be greater than zero")
	}
	if workers < 1 {
		return fmt.Errorf("c must be greater than zero")
	}
	if writes < 0 || writes > 100 {
		return fmt.Errorf("writes must be between 0 and 100")
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("target must be an HTTP or HTTPS URL without credentials, query parameters, or a fragment")
	}
	return nil
}

type summary struct {
	successful uint64
	elapsed    time.Duration
	latencies  []time.Duration
}

func (s summary) percentile(p float64) time.Duration {
	if len(s.latencies) == 0 {
		return 0
	}
	return s.latencies[int(float64(len(s.latencies)-1)*p)]
}

func runLoad(target string, total, workers, writes int) summary {
	jobs := make(chan int)
	lat := make(chan time.Duration, total)
	var ok atomic.Uint64
	client := &http.Client{Timeout: 4 * time.Second}
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				key := fmt.Sprintf("key-%d", i%1000)
				method := http.MethodGet
				var body io.Reader
				if i%100 < writes {
					method = http.MethodPut
					body = bytes.NewBufferString(fmt.Sprintf(`{"value":"value-%d"}`, i))
				}
				t := time.Now()
				req, err := http.NewRequest(method, target+"/kv/"+key, body)
				if err != nil {
					lat <- time.Since(t)
					continue
				}
				if body != nil {
					req.Header.Set("Content-Type", "application/json")
				}
				resp, err := client.Do(req)
				if err == nil {
					_, readErr := io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if readErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
						ok.Add(1)
					}
				}
				lat <- time.Since(t)
			}
		}()
	}
	go func() {
		for i := 0; i < total; i++ {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		close(lat)
	}()
	vals := make([]time.Duration, 0, total)
	for d := range lat {
		vals = append(vals, d)
	}
	elapsed := time.Since(start)
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	return summary{successful: ok.Load(), elapsed: elapsed, latencies: vals}
}
