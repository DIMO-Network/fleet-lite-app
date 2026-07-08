.PHONY: all deps docker clean test fmt lint install migrate migrate-down sqlboiler addmigration push-postmark-templates configure-postmark-webhook

INSTALL_DIR    = $(GOPATH)/bin
DEST_DIR       = ./target
PATHINSTBIN    = $(DEST_DIR)/bin
PATHINSTDOCKER = $(DEST_DIR)/docker

VERSION   := $(shell git describe --tags 2>/dev/null || echo "v0.0.0")
VER_CUT   := $(shell echo $(VERSION) | cut -c2-)
VER_MAJOR := $(shell echo $(VER_CUT) | cut -f1 -d.)
VER_MINOR := $(shell echo $(VER_CUT) | cut -f2 -d.)
DATE      := $(shell date +"%Y-%m-%dT%H:%M:%SZ")
COMMIT    := $(shell git rev-parse --short HEAD)

LD_FLAGS = -X main.CommitHash=$(COMMIT)
GO_FLAGS =

APPS = fleet-lite-app

# Schema name used only for local sqlboiler introspection (must match the local
# dev DB name in sqlboiler.toml). It is stripped from generated models so they
# stay schema-agnostic and resolve via search_path at runtime — see `sqlboiler`.
DB_SCHEMA = fleet_lite_app

all: $(APPS)

install: $(APPS)
	@mkdir -p bin
	@cp $(PATHINSTBIN)/fleet-lite-app ./bin/

deps:
	@go mod tidy
	@go mod vendor

SOURCE_FILES = $(shell find internal -type f -name "*.go")

$(PATHINSTBIN)/%: $(SOURCE_FILES)
	@go build $(GO_FLAGS) -ldflags "$(LD_FLAGS)" -o $@ ./cmd/$*

$(APPS): %: $(PATHINSTBIN)/%

docker-tags:
	@echo "latest,$(VER_CUT),$(VER_MAJOR).$(VER_MINOR),$(VER_MAJOR)" > .tags

docker: deps
	@docker build -f ../Dockerfile .. -t dimozone/fleet-lite-app:$(VER_CUT)
	@docker tag dimozone/fleet-lite-app:$(VER_CUT) dimozone/fleet-lite-app:latest

fmt:
	@go list -f {{.Dir}} ./... | xargs -I{} gofmt -w -s {}
	@go mod tidy

lint:
	@golangci-lint run

test:
	@go test $(GO_FLAGS) -timeout 3m -race ./...

migrate:
	@go run ./cmd/fleet-lite-app migrate

migrate-down:
	@go run ./cmd/fleet-lite-app migrate -down

# Sync repo-stored Postmark templates (api/templates/postmark) up to Postmark.
# Requires POSTMARK_SERVER_TOKEN in settings.yaml. Rerunnable (upserts by alias).
push-postmark-templates:
	@go run ./cmd/fleet-lite-app push-postmark-templates

configure-postmark-webhook:
	@go run ./cmd/fleet-lite-app configure-postmark-webhook

sqlboiler:
	@sqlboiler psql --no-tests --wipe
	# Strip the schema qualifier so generated models are schema-agnostic: the
	# runtime schema is resolved via search_path (= DB.Name from settings), so
	# the same models work whatever the deployment's DB_NAME is. See migrate.go
	# (CREATE SCHEMA IF NOT EXISTS <DB.Name>) and shared db BuildConnectionString.
	@sed -i '' -e 's/\\"$(DB_SCHEMA)\\"\.//g' -e 's/$(DB_SCHEMA)\.//g' internal/db/models/*.go

addmigration:
	@goose -dir internal/db/migrations create rename_me sql

clean:
	rm -rf $(PATHINSTBIN)
	rm -rf $(DEST_DIR)/dist
	rm -rf $(PATHINSTDOCKER)
