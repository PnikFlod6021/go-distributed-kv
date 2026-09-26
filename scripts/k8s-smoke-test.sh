#!/usr/bin/env bash
set -euo pipefail

# CI supplies a disposable kind cluster and the locally loaded :ci image.
kubectl apply -f k8s/namespace.yaml -f k8s/service.yaml -f k8s/pdb.yaml
kubectl set image --local -f k8s/statefulset.yaml kv=go-distributed-kv:ci -o yaml | kubectl apply -f -
kubectl -n distributed-kv rollout status statefulset/kv --timeout=180s

forward_pid=''
trap 'if [[ -n "$forward_pid" ]]; then kill "$forward_pid" 2>/dev/null || true; fi' EXIT
forward() {
    kubectl -n distributed-kv port-forward svc/kv 18080:8080 > /tmp/kv-port-forward.log 2>&1 &
    forward_pid=$!
    for attempt in $(seq 1 30); do
        if curl -fsS http://localhost:18080/healthz > /dev/null; then return; fi
        sleep 1
    done
    cat /tmp/kv-port-forward.log
    return 1
}

forward
curl -fsS -X PUT http://localhost:18080/kv/k8s-smoke -H 'Content-Type: application/json' -d '{"value":"survives"}' | jq -e '.replicas_acknowledged == 2'
curl -fsS http://localhost:18080/kv/k8s-smoke | jq -e '.value == "survives"'
kill "$forward_pid"
wait "$forward_pid" || true
forward_pid=''
kubectl -n distributed-kv rollout restart statefulset/kv
kubectl -n distributed-kv rollout status statefulset/kv --timeout=180s
forward
curl -fsS http://localhost:18080/kv/k8s-smoke | jq -e '.value == "survives"'
echo 'PASS: StatefulSet ready, replicated write/read, PVC recovery after rolling restart'
