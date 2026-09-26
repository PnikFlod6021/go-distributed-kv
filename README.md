# Go Distributed Key-Value Store

A distributed key-value store in Go with a write-ahead log, consistent hashing, replicated writes, and majority write acknowledgements. Includes Docker Compose and Kubernetes configurations, Prometheus metrics, Grafana dashboards, and a concurrent load generator.

## Features

- **Persistence:** append-only write-ahead log with crash replay
- **Partitioning:** 64-virtual-node consistent hash ring
- **Replication:** configurable replication factor; default `2`
- **Write coordination:** writes go through the smallest healthy node, then to the key's replica owners
- **Quorum:** a write succeeds only when a majority of targeted replicas acknowledge it
- **Reads:** reads query the key's owner set and tolerate one unavailable replica
- **Observability:** health, cluster state, Prometheus metrics, and provisioned Grafana dashboard
- **Infrastructure:** Docker Compose plus Kubernetes StatefulSet, headless service, PVCs, probes, and PDB
- **Performance:** concurrent Go load generator reports throughput and p50/p95/p99 latency

Routing selects the lexicographically smallest healthy node as the write coordinator. Health checks do not provide consensus or prevent split-brain writes during a network partition.

## Run the 3-node cluster

Requires Docker with the Compose plugin. Published ports bind to localhost; the nodes talk over the Compose network.

```bash
docker compose up --build
```

Endpoints:

- KV nodes: `localhost:8081`, `8082`, `8083`
- Prometheus: `localhost:9090`
- Grafana: `localhost:3000`

Grafana starts with its upstream demo credentials (`admin` / `admin`). The KV API has no authentication or TLS and is intended for a local, trusted environment.

Write through any node:

```bash
curl -X PUT http://localhost:8082/kv/gpu \
  -H 'Content-Type: application/json' \
  -d '{"value":"H100"}'
```

Read from another node:

```bash
curl http://localhost:8083/kv/gpu
```

A read returns `404` when every owner reports the key is missing. If no owner returns a value and any owner is unreachable or returns an invalid response, the API returns `503` instead. A healthy owner can still serve the read when another owner is unavailable.

Inspect cluster routing:

```bash
curl http://localhost:8081/cluster
```

## Failure test

Write a key, stop one replica, then read from a surviving node:

```bash
curl -X PUT http://localhost:8081/kv/demo -H 'Content-Type: application/json' -d '{"value":"survives"}'
docker compose stop node2
curl http://localhost:8083/kv/demo
```

Restart the node:

```bash
docker compose start node2
```

The WAL replays complete records on restart; an incomplete record causes replay to fail. Replicas do not repair writes missed while offline. With the default replication factor of two, both owners must acknowledge a write; reads can use either surviving owner and may return stale values.

Stop the cluster while keeping its data:

```bash
docker compose down
```

To delete all Compose data volumes and start fresh, use `docker compose down -v` or `make compose-reset`. `make compose-down` preserves them.

## Automated deployment checks

With Docker, Python 3, and Go installed, run:

```bash
python3 -u scripts/smoke-test.py
```

Leave ports 8081–8083, 9090, and 3000 free. The script creates a uniquely named Compose project, verifies cross-node writes and deletes, kills a replica, checks read fallback and write-quorum failure, and restarts all nodes to verify WAL replay. It also checks metrics and monitoring health, seeds 1,000 keys, and runs a mixed load test. It removes its own containers and volumes when finished.

GitHub Actions runs this test plus a separate disposable kind cluster. The Kubernetes job checks that the StatefulSet becomes ready and that a replicated value survives a rolling restart with PVCs. `scripts/k8s-smoke-test.sh` is for that disposable CI cluster; it applies resources in the `distributed-kv` namespace.

## Load test

```bash
go run ./cmd/loadtest -target http://localhost:8081 -n 10000 -c 100 -writes 20
```

Output includes attempted requests/sec, successful and failed counts, and p50/p95/p99 latency. Only complete HTTP 2xx responses count as successful; missing keys and transport or body-read errors count as failures. Latency includes reading the response body and covers every attempt. The request count and concurrency must be positive, and the write percentage must be between 0 and 100. Reads are not prepopulated, so a fresh cluster will report missing-key failures.

For a run without initial missing keys, seed the generator's key range first:

```bash
go run ./cmd/loadtest -target http://localhost:8081 -n 1000 -c 10 -writes 100
go run ./cmd/loadtest -target http://localhost:8081 -n 2000 -c 20 -writes 20
```

See [benchmark notes](docs/BENCHMARKS.md) for a recorded CI run and its limitations.

## Kubernetes

For an existing local kind cluster, build and load the image before applying the manifests:

```bash
docker build -t go-distributed-kv:latest .
kind load docker-image go-distributed-kv:latest
kubectl apply -f k8s/namespace.yaml
kubectl apply -f k8s/
kubectl -n distributed-kv rollout status statefulset/kv --timeout=180s
kubectl -n distributed-kv port-forward svc/kv 8080:8080
```

The StatefulSet uses stable pod DNS, persistent volume claims, readiness/liveness probes, and a PodDisruptionBudget.

## Development

The module requires Go 1.22 or newer and uses only the standard library. For a single node without Docker:

```bash
go run ./cmd/server -addr=:8080 -self-url=http://localhost:8080
```

Data is written to `data/store.wal`. Check the code with `go test ./...`, `go vet ./...`, and `go build ./...`. CI also runs `go test -race ./...`; running that locally requires a supported CGO toolchain.

## Demo release limits

This is a small systems demo, not a production datastore. Health-based coordinator selection does not provide consensus or partition fencing. Reads have no version reconciliation, replicas do not catch up after missed writes, and failed quorum writes may still change a minority of replicas. The WAL has no compaction or automatic repair of incomplete records. Internal endpoints are unauthenticated, and the latency metric is a lifetime average rather than a histogram.

The automated checks cover local Compose and kind deployments. They do not establish multi-host network-partition behavior, cloud storage compatibility, or production capacity.

## Metrics

Each node exposes `/metrics` with:

- `kv_requests_total`
- `kv_read_misses_total`
- `kv_replication_failures_total`
- `kv_keys`
- `kv_request_latency_seconds_avg`

## Architecture

```text
                         client
                            |
                     any KV node
                            |
                     healthy leader
                            |
                  consistent-hash ring
                     /             \
                replica A       replica B
                   |                |
                 WAL              WAL

 Prometheus <------ all nodes ------> Grafana
```
