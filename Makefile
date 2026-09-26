.PHONY: build run test fmt compose-up compose-down compose-reset smoke loadtest k8s-apply k8s-delete
build:
	go build -o server ./cmd/server
run:
	go run ./cmd/server
test:
	go test -race ./...
fmt:
	go fmt ./...
compose-up:
	docker compose up --build
compose-down:
	docker compose down
compose-reset:
	docker compose down -v
smoke:
	python3 -u scripts/smoke-test.py
loadtest:
	go run ./cmd/loadtest -target http://localhost:8081 -n 10000 -c 100 -writes 20
k8s-apply:
	kubectl apply -f k8s/
k8s-delete:
	kubectl delete -f k8s/ --ignore-not-found
