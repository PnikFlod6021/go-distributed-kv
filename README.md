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

```bash
docker compose up --build
```

Endpoints:

- KV nodes: `localhost:8081`, `8082`, `8083`
- Prometheus: `localhost:9090`
- Grafana: `localhost:3000`

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

## Load test

```bash
go run ./cmd/loadtest -target http://localhost:8081 -n 10000 -c 100 -writes 20
```

Output includes requests/sec plus p50, p95, and p99 latency. The current generator counts HTTP responses below 500 as successful, including missing-key responses. Latency measures time until response headers arrive.

## Kubernetes

Build the image into your local cluster's image environment, then:

```bash
kubectl apply -f k8s/
kubectl -n distributed-kv port-forward svc/kv 8080:8080
```

The StatefulSet uses stable pod DNS, persistent volume claims, readiness/liveness probes, and a PodDisruptionBudget.

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
