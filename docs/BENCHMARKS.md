# Load-test notes

This is a short functional load run, not a capacity estimate. All three nodes, Prometheus, Grafana, and the load generator ran on one GitHub-hosted Ubuntu 24.04 runner. It reported four CPUs, Linux 6.17.0-1022-azure x86_64, and Go 1.22.12. The nodes used the Compose defaults: two replicas and synchronous WAL writes.

Recorded on 2026-09-26 UTC, from [the deployment-check run](https://github.com/PnikFlod6021/go-distributed-kv/actions/runs/36204023225/job/108296564654) for commit `4c94c258bd76a39f8d26e8435e5932fd896e75c2`.

The script first seeded all 1,000 keys used by the generator:

```text
requests=1000 successful=1000 failed=0 concurrency=10 elapsed=597.339012ms throughput=1674.1 req/s
p50=4.681275ms p95=12.999909ms p99=24.973467ms
```

It then ran 2,000 requests at concurrency 20 with 20% writes:

```text
requests=2000 successful=2000 failed=0 concurrency=20 elapsed=603.362421ms throughput=3314.8 req/s
p50=3.774137ms p95=17.607994ms p99=33.909337ms
```

Latency includes reading response bodies. All attempts are included in the latency samples, and only complete HTTP 2xx responses count as successful. The script fails if either run has a failed request.

## Reproduce

With Docker Compose, Python 3, and Go installed, and ports 8081–8083, 9090, and 3000 free:

```bash
python3 -u scripts/smoke-test.py
```

The script creates and removes its own Compose project and volumes. It checks failure and restart behavior before running the load generator. To measure a longer workload against an existing cluster, seed the keys and raise `-n`; report the command, hardware, error counts, and duration alongside the result.

## What this does not measure

The measured phase lasted about 0.6 seconds. It is one run on a shared CI host, with a small warmed key set and all traffic on one machine. There are no repeated trials, sustained-load results, cross-host network costs, or disk saturation measurements. The numbers show that the end-to-end load exercise succeeded; they are not a production throughput claim or a comparison with other databases.
