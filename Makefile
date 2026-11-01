GOLANGCI_LINT_VERSION := v2.15.0

GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: test
test:
	go test ./...

.PHONY: lint
lint:	## Find any linting issues in the project
	$(GOLANGCI_LINT) run --timeout=5m

.PHONY: lint-fix
lint-fix:	## Fix any linting issues in the project
	$(GOLANGCI_LINT) run --fix --timeout=5m
