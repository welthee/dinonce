DIST_DIR := ./dist

OAPI_SCHEMA_FILE := api/api.yaml
DEEPMAP_CONFIG_FILE := api/deepmap/api.yaml
OAPI_GENERATED_DIR := ./internal/api/generated
OAPI_CODEGEN := ~/go/bin/oapi-codegen

all: clean oapi build

mod-download:
		go mod download

build:
		mkdir -p $(DIST_DIR)
		go build -o $(DIST_DIR)/dinonce cmd/dinonce/main.go

.PHONY: oapi clean

oapi:
		mkdir -p $(OAPI_GENERATED_DIR)
		$(OAPI_CODEGEN) --config=$(DEEPMAP_CONFIG_FILE) $(OAPI_SCHEMA_FILE)

start-postgres:
	docker run \
		--rm \
		--name dinonce-postgres \
    -p 5433:5432 \
    -e POSTGRES_USER=postgres \
    -e POSTGRES_PASSWORD=postgres \
    -e POSTGRES_DB=postgres \
    -d postgres

stop-postgres:
	docker stop dinonce-postgres

clean:
		rm -rf $(DIST_DIR)
		rm -rf $(OAPI_GENERATED_DIR)
