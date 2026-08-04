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
	bash scripts/tests/decision-e2e.sh

.PHONY: gen
gen: ## Run every project's code generators
	$(NX) run-many --target=generate --all

.PHONY: graph
graph: ## Open the single Go plus Angular dependency graph
	$(NX) graph
