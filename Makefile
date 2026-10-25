.PHONY: fmt vet test test-integration pre-commit compose-up compose-down build

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

compose-up:
	docker compose -f docker/compose.yaml up -d --wait

compose-down:
	docker compose -f docker/compose.yaml down -v

test-integration:
	ARTIFACT_REGISTRY_TEST_POSTGRES_DSN='postgresql://postgres:postgres@127.0.0.1:5432/artifact_registry?sslmode=disable' \
	ARTIFACT_REGISTRY_TEST_S3_ENDPOINT='127.0.0.1:9000' \
	ARTIFACT_REGISTRY_TEST_MIGRATOR_PASSWORD='artifact-registry-migrator-test-only' \
	ARTIFACT_REGISTRY_TEST_RUNTIME_PASSWORD='artifact-registry-runtime-test-only' \
	ARTIFACT_REGISTRY_TEST_MAINTENANCE_PASSWORD='artifact-registry-maintenance-test-only' \
	ARTIFACT_REGISTRY_TEST_READINESS_PASSWORD='artifact-registry-readiness-test-only' \
	go test -race -count=1 -timeout=10m ./...

build:
	go build ./cmd/...

pre-commit: vet test
	git diff --check
