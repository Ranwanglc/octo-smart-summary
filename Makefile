.PHONY: build test docker-build run-api run-worker

build:
	go build ./...

test:
	go test -v -count=1 ./...

docker-build:
	docker build -f Dockerfile.api -t smart-summary-api:local .
	docker build -f Dockerfile.worker -t smart-summary-worker:local .

run-api:
	go run ./cmd/summary-api

run-worker:
	go run ./cmd/summary-worker
