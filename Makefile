SHELL := /usr/bin/env bash
export PATH := $(CURDIR)/scripts/bin:$(PATH)

COMPOSE ?= docker compose
NX ?= npx nx

.DEFAULT_GOAL := help

.PHONY: help
help: ## List the available targets
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

.PHONY: up
up: ## Build and start the whole control plane, waiting for health
	$(COMPOSE) up --build --detach
	bash scripts/compose-wait.sh

.PHONY: down
down: ## Stop the control plane and remove its containers
	$(COMPOSE) down --remove-orphans

.PHONY: clean
clean: ## Remove containers, named volumes, local images and build caches
	$(COMPOSE) down --remove-orphans --volumes --rmi local
	# The Go module cache is written read-only by the toolchain container.
	@chmod -R u+w .gocache 2>/dev/null || true
	rm -rf dist tmp .nx .angular .gocache

.PHONY: test
test: ## Run every project's tests plus the compose contract
	$(NX) run-many --target=test --all
	python3 scripts/tests/compose-contract.py

.PHONY: ci
ci: ## Run exactly what CI runs, so a green local run means a green pipeline
	$(NX) run-many --target=lint --all
	$(NX) run-many --target=test --all
	$(NX) run-many --target=build --all
	python3 scripts/tests/compose-contract.py
	bash scripts/tests/nx-affected-isolation.sh

.PHONY: smoke
smoke: ## Verify a running stack end to end
	bash scripts/tests/stack-smoke.sh

.PHONY: gen
gen: ## Run every project's code generators
	$(NX) run-many --target=generate --all

.PHONY: graph
graph: ## Open the single Go plus Angular dependency graph
	$(NX) graph
