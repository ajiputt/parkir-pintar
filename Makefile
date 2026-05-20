## ParkirPintar — Makefile
## Convention: lowercase phony targets, grouped per workflow

SHELL := /bin/bash
.DEFAULT_GOAL := help

GO ?= go
DOCKER_COMPOSE ?= docker compose
COMPOSE_FILE := deploy/docker/docker-compose.yml
# Active services. search & presence di-defer (lihat ADR-0009),
# kontrak proto tetap di proto/search/v1/ & proto/presence/v1/.
SERVICES := gateway reservation billing payment notification

# ----- Help -------------------------------------------------------------------

.PHONY: help
help: ## Daftar target Makefile ini
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		sort | awk 'BEGIN{FS=":.*?## "} {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}'

# ----- Tooling ----------------------------------------------------------------

.PHONY: tools
tools: ## Install dev tools (buf, protoc-gen-go, golangci-lint, mockgen, sqlc, migrate)
	$(GO) install github.com/bufbuild/buf/cmd/buf@v1.34.0
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
	$(GO) install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.20.0
	$(GO) install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@v2.20.0
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.59.1
	$(GO) install go.uber.org/mock/mockgen@v0.6.0
	$(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0
	$(GO) install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.17.1

# ----- Proto / Codegen --------------------------------------------------------

.PHONY: proto
proto: ## Generate Go code dari .proto (gRPC + REST gateway + OpenAPI)
	cd proto && buf generate

.PHONY: proto-lint
proto-lint: ## Lint proto files
	cd proto && buf lint

.PHONY: proto-breaking
proto-breaking: ## Cek breaking change vs main branch
	cd proto && buf breaking --against '../.git#branch=main,subdir=proto'

.PHONY: tidy
tidy: ## Run go mod tidy di semua module workspace
	@for m in pkg proto services/gateway services/reservation services/billing services/payment services/notification; do \
		echo "==> tidy $$m"; \
		(cd $$m && $(GO) mod tidy); \
	done

.PHONY: mod-tidy
mod-tidy: tidy ## Alias 'tidy' — sesuai convention boilerplate

.PHONY: mod-reset
mod-reset: ## Reset go.sum + clean module cache + tidy (untuk recovery dari corrupt state)
	@for m in pkg proto services/gateway services/reservation services/billing services/payment services/notification; do \
		echo "==> reset $$m"; \
		(cd $$m && rm -f go.sum && $(GO) clean --modcache && $(GO) mod tidy) || true; \
	done

.PHONY: regen
regen: proto tidy ## Regenerate proto + sync go.mod (alias buat workflow setelah edit .proto)

# ----- Build & Lint -----------------------------------------------------------

.PHONY: build
build: ## Build semua service ke ./bin/
	@for svc in $(SERVICES); do \
		echo "==> build $$svc"; \
		(cd services/$$svc && CGO_ENABLED=0 $(GO) build -o ../../bin/$$svc ./cmd/...); \
	done

.PHONY: lint
lint: ## golangci-lint untuk semua module
	golangci-lint run ./... --timeout 5m

.PHONY: vet
vet: ## go vet semua module
	@for m in pkg proto services/*; do \
		echo "==> vet $$m"; \
		(cd $$m && $(GO) vet ./...); \
	done

# ----- Tests ------------------------------------------------------------------

.PHONY: test
test: test-unit ## Alias test-unit

.PHONY: test-unit
test-unit: ## Unit test semua module dengan -race + coverage
	@for m in pkg services/reservation services/billing services/payment; do \
		echo "==> unit test $$m"; \
		(cd $$m && $(GO) test -race -coverprofile=coverage.out -covermode=atomic ./...); \
	done

.PHONY: test-integration
test-integration: ## Integration test (testcontainers — butuh Docker)
	cd test && $(GO) test -tags=integration -race -timeout=10m ./integration/...

.PHONY: test-e2e
test-e2e: ## E2E test terhadap stack docker-compose
	./scripts/e2e.sh

.PHONY: test-load
test-load: ## Load test dengan k6 (butuh k6 installed)
	k6 run test/load/reservation.js

.PHONY: cover
cover: test-unit ## Tampilkan coverage HTML
	$(GO) tool cover -html=pkg/coverage.out

.PHONY: unit-test-coverage
unit-test-coverage: ## Unit test dengan coverage report (boilerplate convention)
	@echo "==> Running unit tests with coverage..."
	@for m in pkg services/reservation services/billing services/payment services/notification services/gateway; do \
		echo "==> coverage $$m"; \
		(cd $$m && $(GO) test -v -covermode=count ./... -coverprofile=coverage.cov 2>&1 | tail -50); \
	done
	@echo "==> Coverage per-package summary:"
	@for m in pkg services/reservation services/billing services/payment services/notification services/gateway; do \
		echo ""; echo "===== $$m ====="; \
		(cd $$m && $(GO) tool cover -func=coverage.cov 2>/dev/null | tail -1); \
	done

.PHONY: check-cognitive-complexity
check-cognitive-complexity: ## Cek cognitive complexity (gocognit, threshold 15) — boilerplate convention
	@command -v gocognit >/dev/null 2>&1 || { echo "Install: go install github.com/uudashr/gocognit/cmd/gocognit@latest"; exit 1; }
	@find . -type f -name '*.go' \
		-not -name "*_test.go" \
		-not -name "*_mock.go" \
		-not -path "./_mock/*" \
		-not -path "*/gen/*" \
		-not -path "./proto/gen/*" \
		-exec gocognit -over 15 {} \;

# ----- Database ---------------------------------------------------------------

.PHONY: migrate-up
migrate-up: ## Jalankan migration semua schema (reservation, billing, payment, notification)
	@for s in reservation billing payment notification; do \
		echo "==> migrate $$s"; \
		migrate -path deploy/migrations/$$s -database "$(DB_URL)&search_path=$$s" up; \
	done

.PHONY: migrate-down
migrate-down: ## Rollback 1 migration per schema
	@for s in reservation billing payment notification; do \
		migrate -path deploy/migrations/$$s -database "$(DB_URL)&search_path=$$s" down 1; \
	done

.PHONY: helm-sync-migrations
helm-sync-migrations: ## Sync deploy/migrations/ ke chart dir (untuk local helm template/install)
	@rm -rf deploy/helm/parkir-pintar/migrations
	@mkdir -p deploy/helm/parkir-pintar/migrations
	@cp -r deploy/migrations/* deploy/helm/parkir-pintar/migrations/
	@echo "==> Migrations synced ke deploy/helm/parkir-pintar/migrations/"

.PHONY: seed
seed: ## Seed parking area + 5 floor + 750 spot
	./scripts/seed.sh

# ----- Demo / Compose ---------------------------------------------------------

.PHONY: demo-up
demo-up: ## Boot full stack demo (postgres, redis, nats, jaeger, grafana, all services)
	$(DOCKER_COMPOSE) -f $(COMPOSE_FILE) up -d --build

.PHONY: demo-down
demo-down: ## Tear down stack demo
	$(DOCKER_COMPOSE) -f $(COMPOSE_FILE) down -v

.PHONY: demo-logs
demo-logs: ## Tail logs semua service
	$(DOCKER_COMPOSE) -f $(COMPOSE_FILE) logs -f

.PHONY: demo-wait
demo-wait: ## Tunggu sampai stack healthy
	./scripts/wait-healthy.sh

# ----- Security ---------------------------------------------------------------

.PHONY: sec-scan
sec-scan: ## gosec + govulncheck + trivy
	gosec ./... || true
	govulncheck ./... || true
	@for svc in $(SERVICES); do \
		trivy image --severity HIGH,CRITICAL parkir-pintar/$$svc:latest || true; \
	done

.PHONY: gosec
gosec: ## Run gosec dengan output sonarqube format (boilerplate convention)
	@command -v gosec >/dev/null 2>&1 || { echo "Install: go install github.com/securego/gosec/v2/cmd/gosec@latest"; exit 1; }
	gosec -exclude=G401,G304,G501,G505 -fmt=sonarqube -out=sonar-gosec.json ./... || true
	@echo "==> Output: sonar-gosec.json"

.PHONY: golint
golint: ## Alias 'lint' — sesuai convention boilerplate
	golangci-lint run --timeout 5m ./...

# ----- AWS Operations --------------------------------------------------------

AWS_ENV ?= demo

.PHONY: aws-wake
aws-wake: ## Hidupkan AWS stack (start of working day)
	./scripts/aws-wake.sh $(AWS_ENV)

.PHONY: aws-sleep
aws-sleep: ## Matikan AWS stack (end of working day, hemat $)
	./scripts/aws-sleep.sh $(AWS_ENV)

.PHONY: aws-deploy
aws-deploy: ## Deploy image baru ke AWS (without terraform apply)
	gh workflow run deploy-aws.yml -f environment=$(AWS_ENV) -f apply_terraform=false

.PHONY: aws-deploy-infra
aws-deploy-infra: ## Apply terraform + deploy
	gh workflow run deploy-aws.yml -f environment=$(AWS_ENV) -f apply_terraform=true

.PHONY: aws-teardown
aws-teardown: ## DESTROY AWS infra (snapshot dulu)
	gh workflow run aws-teardown.yml -f environment=$(AWS_ENV) -f confirm=DESTROY

.PHONY: aws-cost
aws-cost: ## Tampilkan biaya AWS bulan ini
	aws ce get-cost-and-usage \
	  --time-period Start=$$(date -d 'first day of this month' +%Y-%m-%d),End=$$(date +%Y-%m-%d) \
	  --granularity MONTHLY \
	  --metrics UnblendedCost \
	  --filter '{"Tags":{"Key":"Project","Values":["parkir-pintar"]}}' \
	  --query 'ResultsByTime[0].Total.UnblendedCost' --output table

# ----- Mocks ------------------------------------------------------------------

.PHONY: gen-mock-source
gen-mock-source: ## Generate single mock dari source file (boilerplate convention)
                 ## Usage: make gen-mock-source pkg=<pkg> destination=<dest> source=<src>
	@command -v mockgen >/dev/null 2>&1 || { echo "Install: go install go.uber.org/mock/mockgen@latest"; exit 1; }
	@if [ -z "$(pkg)" ] || [ -z "$(destination)" ] || [ -z "$(source)" ]; then \
		echo "Usage: make gen-mock-source pkg=mock_repository source=path/to/file.go destination=_mock/path/mock.go"; \
		exit 1; \
	fi
	mockgen -package=$(pkg) -destination=$(destination) -source=$(source)
	@echo "==> Generated: $(destination)"

.PHONY: gen-mocks
gen-mocks: ## Generate semua mocks via go:generate directives (Phase C — full project)
	@command -v mockgen >/dev/null 2>&1 || { echo "Install: go install go.uber.org/mock/mockgen@latest"; exit 1; }
	@for m in pkg services/gateway services/reservation services/billing services/payment services/notification; do \
		echo "==> generate mocks for $$m"; \
		(cd $$m && $(GO) generate ./...); \
	done

# ----- Clean ------------------------------------------------------------------

.PHONY: clean
clean: ## Hapus build artifacts
	rm -rf bin/ coverage.out coverage.cov sonar-gosec.json **/coverage.out **/coverage.cov
