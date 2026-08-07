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
	# The ADS reads the role matrix from the database, so the schema and the
	# demo matrix have to be there before the stack can answer anything. Both
	# steps are idempotent, so `make up` on a running stack is a no-op.
	$(MAKE) migrate
	$(MAKE) seed

.PHONY: down
down: ## Stop the control plane and remove its containers
	$(COMPOSE) down --remove-orphans

.PHONY: clean
clean: ## Remove containers, named volumes, local images and build caches
	$(COMPOSE) down --remove-orphans --volumes --rmi local
	# The Go module cache is written read-only by the toolchain container.
	@chmod -R u+w .gocache 2>/dev/null || true
	rm -rf dist tmp .nx .angular .gocache

.PHONY: policy-test
policy-test: ## Compile the Cerbos policies and run the policy test suite
	bash scripts/cerbos.sh compile --tests=/policies/tests /policies
	# The ADR-003 control experiment reproduces the cross-role hazard on purpose,
	# so it is compiled from its own directory and never served to the PDP.
	POLICY_DIR=deploy/cerbos/control bash scripts/cerbos.sh compile --tests=/policies/tests /policies

.PHONY: catalog-gen
catalog-gen: ## Regenerate the FHIR catalog, policies, schemas, tests and DB seed from the manifest
	bash scripts/go.sh libs/cataloggen run ./cmd/cataloggen -root /workspace

.PHONY: catalog-check
catalog-check: ## Fail if the committed catalog tree drifted from libs/cataloggen/manifest.yaml
	bash scripts/go.sh libs/cataloggen run ./cmd/cataloggen -root /workspace -check

.PHONY: capability-gen
capability-gen: ## Regenerate the composite UI capability catalog and its DB seed
	bash scripts/go.sh libs/capabilitycatalog run ./cmd/capabilitycatalog-gen -root /workspace

.PHONY: capability-check
capability-check: ## Fail if the UI capability catalog drifted or fails validation
	bash scripts/go.sh libs/capabilitycatalog run ./cmd/capabilitycatalog-gen -root /workspace -check

.PHONY: test
test: ## Run every project's tests, the policy suite and the compose contract
	$(NX) run-many --target=test --all
	$(MAKE) policy-test
	python3 scripts/tests/compose-contract.py

.PHONY: ci
ci: ## Run exactly what CI runs, so a green local run means a green pipeline
	$(NX) run-many --target=lint --all
	$(NX) run-many --target=test --all
	$(NX) run-many --target=build --all
	$(MAKE) catalog-check
	$(MAKE) capability-check
	$(MAKE) policy-test
	python3 scripts/tests/compose-contract.py
	bash scripts/tests/nx-affected-isolation.sh

.PHONY: migrate
migrate: ## Apply the authorization schema to PostgreSQL
	bash scripts/liquibase.sh postgres update

.PHONY: migrate-oracle
migrate-oracle: ## Apply the same schema to Oracle, starting it if needed
	$(COMPOSE) --profile oracle up --detach oracle
	bash scripts/oracle-wait.sh
	bash scripts/liquibase.sh oracle update

.PHONY: policy-release-up
policy-release-up: ## Start Gitea, a managed Cerbos and the policy controller (issue #21)
	$(COMPOSE) --profile policy-release up --build --detach gitea postgres cerbos-managed
	bash scripts/compose-wait.sh gitea postgres cerbos-managed
	$(MAKE) policy-release-seed
	$(COMPOSE) --profile policy-release up --build --detach policy-controller

.PHONY: policy-release-seed
policy-release-seed: ## Seed Gitea with the root policy repository and a protected tag
	bash scripts/gitea-seed.sh

.PHONY: policy-release-down
policy-release-down: ## Stop the policy-release profile's services
	$(COMPOSE) --profile policy-release down --remove-orphans

.PHONY: observability-up
observability-up: ## Start Prometheus and Grafana, scraping the ADS and Cerbos (issue #23)
	$(COMPOSE) --profile observability up --build --detach prometheus grafana

.PHONY: observability-down
observability-down: ## Stop the observability profile's services
	$(COMPOSE) --profile observability down --remove-orphans

.PHONY: seed
seed: ## Write the demo role matrix into the authorization database
	bash scripts/seed.sh postgres

.PHONY: db-test
db-test: ## Run the store contract against PostgreSQL
	bash scripts/tests/migration-contract.sh postgres
	bash scripts/tests/store-contract.sh postgres

.PHONY: db-test-dual
db-test-dual: ## Prove portability: the same contract against both engines
	bash scripts/tests/migration-contract.sh postgres
	$(COMPOSE) --profile oracle up --detach oracle
	bash scripts/oracle-wait.sh
	bash scripts/tests/migration-contract.sh oracle
	bash scripts/tests/store-contract.sh dual

.PHONY: smoke
smoke: ## Verify a running stack end to end
	bash scripts/tests/stack-smoke.sh
	bash scripts/tests/identity-e2e.sh
	bash scripts/tests/decision-e2e.sh

.PHONY: gen
gen: ## Run every project's code generators
	$(NX) run-many --target=generate --all

.PHONY: graph
graph: ## Open the single Go plus Angular dependency graph
	$(NX) graph
