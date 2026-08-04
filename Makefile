OPENAPI_VALIDATE_VERSION := v0.138.0
ACTIONLINT_VERSION := v1.7.12
GOVULNCHECK_VERSION := v1.3.0

.PHONY: build run test contract-test openapi-generate openapi-validate actionlint govulncheck verify clean deps create-user help

build:
	go build -o denkit-stash .

run: build
	./denkit-stash

deps:
	go mod tidy
	go mod download

create-user: build
	./denkit-stash -create-user=testuser

test:
	go test ./...

contract-test:
	./scripts/contract-test.sh

openapi-generate:
	go run -tags openapi openapi_export.go http_api.go

openapi-validate:
	go run github.com/getkin/kin-openapi/cmd/validate@$(OPENAPI_VALIDATE_VERSION) docs/openapi.yaml

actionlint:
	go run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) .github/workflows/*.yml

govulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

verify: openapi-generate openapi-validate actionlint govulncheck
	test -z "$$(gofmt -l .)"
	go test ./...

clean:
	rm -f denkit-stash denkit-stash.exe
	rm -rf storage/

help:
	@echo "Available targets:"
	@echo "  build      - Build the server binary"
	@echo "  run        - Build and run the server"
	@echo "  deps       - Install Go dependencies"
	@echo "  create-user - Create a test user"
	@echo "  test       - Run Go tests"
	@echo "  contract-test - Run black-box butler compatibility checks"
	@echo "  openapi-generate - Generate OpenAPI docs from Huma routes"
	@echo "  openapi-validate - Validate generated OpenAPI docs"
	@echo "  actionlint - Validate GitHub Actions workflow syntax"
	@echo "  govulncheck - Scan reachable Go code for known vulnerabilities"
	@echo "  verify     - Run local supply-chain and test gates"
	@echo "  clean      - Clean build artifacts"
	@echo "  help       - Show this help"
