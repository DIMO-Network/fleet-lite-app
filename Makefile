# fleet-lite-app — root Makefile for local development.
#
# `make dev` is the one-command path for a new developer: it checks the dev
# host is in /etc/hosts, ensures a local Postgres role + database exist, copies
# settings, installs web deps, then brings up the frontend (Vite + mkcert) and
# the backend (Go) together. Ctrl-C tears both down.
#
# See README.md for the manual, step-by-step equivalent.

SHELL    := /bin/bash
DEV_HOST := local-fleet-lite.dimo.org
WEB_PORT := 3009
API_PORT := 8084
DB_NAME  := fleet_lite_app
DB_USER  := dimo
DB_PASS  := dimo

.DEFAULT_GOAL := help
.PHONY: dev help check-host db settings migrate web-install web api

## dev: bring up frontend + backend together (one-command local dev)
dev: check-host db settings migrate web-install
	@scripts/dev-up.sh

## check-host: verify $(DEV_HOST) resolves to localhost via /etc/hosts
# NB: we deliberately do NOT use nslookup here — on macOS it queries DNS only
# and ignores /etc/hosts, so it returns NXDOMAIN even when the entry is present.
# dscacheutil (macOS) / getent (Linux) both consult /etc/hosts.
check-host:
	@ip="$$(getent ahostsv4 $(DEV_HOST) 2>/dev/null | awk 'NR==1{print $$1}')"; \
	if [ -z "$$ip" ]; then \
	  ip="$$(dscacheutil -q host -a name $(DEV_HOST) 2>/dev/null | awk '/^ip_address:/{print $$2; exit}')"; \
	fi; \
	if [ "$$ip" = "127.0.0.1" ] || [ "$$ip" = "::1" ]; then \
	  echo "✓ $(DEV_HOST) → $$ip"; \
	else \
	  echo "✗ $(DEV_HOST) does not resolve to localhost (got: $${ip:-nothing})."; \
	  echo "  Add it to /etc/hosts, then flush the DNS cache:"; \
	  echo "    echo '127.0.0.1 $(DEV_HOST)' | sudo tee -a /etc/hosts"; \
	  echo "    sudo killall -HUP mDNSResponder"; \
	  exit 1; \
	fi

## db: ensure psql is installed, Postgres is reachable, and role + db exist
db:
	@command -v psql >/dev/null 2>&1 || { \
	  echo "✗ psql not found — install Postgres (client + server) with Homebrew:"; \
	  echo "    brew install postgresql@16"; \
	  echo "    brew services start postgresql@16"; \
	  exit 1; }
	@PGCONNECT_TIMEOUT=5 psql -w -h localhost -p 5432 -d postgres -tAc 'SELECT 1' >/dev/null 2>&1 || { \
	  echo "✗ psql is installed but can't connect to Postgres on localhost:5432."; \
	  echo "  Install and/or start the server with Homebrew:"; \
	  echo "    brew install postgresql@16"; \
	  echo "    brew services start postgresql@16"; \
	  echo "  (check it's running with: brew services list)"; \
	  exit 1; }
	@psql -h localhost -d postgres -tAc "SELECT 1 FROM pg_roles WHERE rolname='$(DB_USER)'" | grep -q 1 || { \
	  echo "▶ creating role '$(DB_USER)'…"; \
	  psql -h localhost -d postgres -c "CREATE ROLE $(DB_USER) WITH LOGIN PASSWORD '$(DB_PASS)';"; }
	@psql -h localhost -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='$(DB_NAME)'" | grep -q 1 || { \
	  echo "▶ creating database '$(DB_NAME)'…"; \
	  psql -h localhost -d postgres -c "CREATE DATABASE $(DB_NAME) WITH OWNER $(DB_USER);"; }
	@PGPASSWORD=$(DB_PASS) PGCONNECT_TIMEOUT=5 psql -w -h localhost -U $(DB_USER) -d $(DB_NAME) -tAc 'SELECT 1' >/dev/null 2>&1 || { \
	  echo "✗ role '$(DB_USER)' can't connect to '$(DB_NAME)' with the password the app expects."; \
	  echo "  The backend connects as $(DB_USER)/$(DB_PASS). Reset the role password with:"; \
	  echo "    psql -h localhost -d postgres -c \"ALTER ROLE $(DB_USER) WITH LOGIN PASSWORD '$(DB_PASS)';\""; \
	  exit 1; }
	@echo "✓ postgres ready ($(DB_USER) can connect to $(DB_NAME))"

## settings: create api/settings.yaml from the sample if it's missing
settings:
	@if [ ! -f api/settings.yaml ]; then \
	  cp api/settings.sample.yaml api/settings.yaml; \
	  echo "✓ created api/settings.yaml from sample"; \
	else \
	  echo "✓ api/settings.yaml present"; \
	fi

## migrate: run backend database migrations
migrate:
	@echo "▶ running migrations…"
	@cd api && go run ./cmd/fleet-lite-app migrate

## web-install: install frontend dependencies if missing
web-install:
	@if [ ! -d web/node_modules ]; then \
	  echo "▶ installing web deps…"; cd web && npm install; \
	else \
	  echo "✓ web deps present (run 'cd web && npm install' to refresh)"; \
	fi

## web: run only the frontend (Vite dev server, generates mkcert certs)
web: check-host web-install
	@cd web && npm run dev

## api: run only the backend (requires certs from a running/previous 'make web')
api: settings migrate
	@cd api && go run ./cmd/fleet-lite-app

## help: list targets
help:
	@echo "fleet-lite-app — local dev"
	@echo ""
	@echo "  make dev          bring up frontend + backend together (start here)"
	@echo "  make check-host   verify $(DEV_HOST) is in /etc/hosts"
	@echo "  make db           ensure local postgres role '$(DB_USER)' + db '$(DB_NAME)' exist"
	@echo "  make migrate      run backend migrations"
	@echo "  make web          run only the frontend  (https://$(DEV_HOST):$(WEB_PORT))"
	@echo "  make api          run only the backend   (api on :$(API_PORT))"
	@echo ""
	@echo "First run also needs the mkcert root CA trusted — Vite installs it on"
	@echo "first 'npm run dev' (one-time sudo prompt)."
