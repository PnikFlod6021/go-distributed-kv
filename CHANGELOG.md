# Changelog

## 0.1.0 — first demo release

- WAL-backed storage with replay coverage for large values and deleted keys.
- Consistent-hash replica placement, health-based write routing, and majority write acknowledgements.
- HTTP API with replica read failures reported separately from missing keys.
- Docker Compose and Kubernetes examples, plus Prometheus and Grafana configuration.
- Load generator with argument validation, failure counts, and full-response latency measurements.
- CI checks for formatting, vet, race tests, build, Compose failure/restart behavior, and Kubernetes PVC recovery.
- Reproducible load-test instructions and a recorded CI measurement.

This release is for local demonstrations and experiments. It does not provide consensus, replica repair, authentication, or production operational guarantees. See the README for the full limitations.
