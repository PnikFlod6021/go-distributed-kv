#!/usr/bin/env python3
"""Run a disposable Compose cluster through failure, recovery, and load checks."""

import json
import os
import platform
import re
import subprocess
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
ENV = dict(os.environ, COMPOSE_PROJECT_NAME="kv-smoke-" + uuid.uuid4().hex[:8])
NODES = {f"node-{i}": f"http://localhost:{8080 + i}" for i in range(1, 4)}


def compose(*args, check=True):
    return subprocess.run(
        ["docker", "compose", *args], cwd=ROOT, env=ENV, check=check, timeout=300
    )


def request(url, method="GET", value=None, expected=200):
    data = None if value is None else json.dumps({"value": value}).encode()
    req = urllib.request.Request(url, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        response = urllib.request.urlopen(req, timeout=10)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        body = response.read().decode()
        if response.status != expected:
            raise AssertionError(f"{method} {url}: expected {expected}, got {response.status}: {body}")
        if response.headers.get_content_type() == "application/json":
            return json.loads(body)
        return body


def wait_ready(url):
    deadline = time.monotonic() + 90
    while True:
        try:
            request(url)
            return
        except (OSError, AssertionError):
            if time.monotonic() >= deadline:
                raise
            time.sleep(1)


def check_value(base, path, expected):
    actual = request(base + path)
    if not isinstance(actual, dict) or actual.get("value") != expected:
        raise AssertionError(f"Unexpected value from {base}{path}: {actual}")


def loadtest(total, workers, writes):
    command = ["go", "run", "./cmd/loadtest", "-target", NODES["node-1"],
               "-n", str(total), "-c", str(workers), "-writes", str(writes)]
    result = subprocess.run(command, cwd=ROOT, check=True, capture_output=True, text=True, timeout=180)
    print(result.stdout, end="", flush=True)
    counts = re.search(r"requests=(\d+) successful=(\d+) failed=(\d+)", result.stdout)
    if not counts or tuple(map(int, counts.groups())) != (total, total, 0):
        raise AssertionError("Load run had failed requests")


def main():
    print(f"Disposable project: {ENV['COMPOSE_PROJECT_NAME']}", flush=True)
    try:
        compose("up", "-d", "--build")
        for base in NODES.values():
            wait_ready(base + "/healthz")
        wait_ready("http://localhost:9090/-/ready")
        wait_ready("http://localhost:3000/api/health")
        metrics = request(NODES["node-1"] + "/metrics")
        if "kv_requests_total" not in metrics:
            raise AssertionError("Missing metrics")

        # Route through a follower to exercise forwarding, too.
        written = request(NODES["node-2"] + "/kv/smoke", "PUT", "survives")
        if written["replicas_acknowledged"] != 2:
            raise AssertionError(f"Expected two replicas: {written}")
        for base in NODES.values():
            check_value(base, "/kv/smoke", "survives")
        request(NODES["node-2"] + "/kv/deleted", "PUT", "remove me")
        request(NODES["node-3"] + "/kv/deleted", "DELETE")
        for base in NODES.values():
            request(base + "/kv/deleted", expected=404)

        owner = written["owners"][0]["id"]
        service = owner.replace("-", "")
        survivor = next(base for node, base in NODES.items() if node != owner)
        compose("kill", "-s", "SIGKILL", service)
        check_value(survivor, "/kv/smoke", "survives")
        # Keep the same value: failed quorum writes can still reach a minority.
        request(survivor + "/kv/smoke", "PUT", "survives", expected=503)
        compose("start", service)
        wait_ready(NODES[owner] + "/healthz")
        check_value(NODES[owner], "/internal/value/smoke", "survives")

        compose("restart", "node1", "node2", "node3")
        for base in NODES.values():
            wait_ready(base + "/healthz")
        for base in NODES.values():
            check_value(base, "/kv/smoke", "survives")
            request(base + "/kv/deleted", expected=404)
        print("PASS: cross-node writes/deletes, replica kill, quorum failure, WAL restart, metrics and monitoring health", flush=True)

        print(f"Benchmark environment: {platform.platform()}, CPUs={os.cpu_count()}", flush=True)
        subprocess.run(["go", "version"], check=True)
        print("Seed 1000 keys (100% writes):", flush=True)
        loadtest(1000, 10, 100)
        print("Measured run (2000 requests, concurrency 20, 20% writes):", flush=True)
        loadtest(2000, 20, 20)
    except Exception:
        compose("logs", "--no-color", "--tail", "100", check=False)
        raise
    finally:
        # Only remove volumes owned by this randomly named test project.
        compose("down", "--volumes", "--remove-orphans")


if __name__ == "__main__":
    main()
