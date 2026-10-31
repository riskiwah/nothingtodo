MAIN_BIN=nothingtodo
BUILD_DIR=./dist
COMMIT_HASH=$(shell git rev-parse --short HEAD)
BUILD_TIMESTAMP=$(shell date '+%Y-%m-%dT%H:%M:%S')
YEAR=$(shell date '+%Y')

LDFLAGS := -s -w -X 'main.commit=$(COMMIT_HASH)' \
		   -X 'main.buildTimestamp=$(BUILD_TIMESTAMP)' \
		   -X 'main.year=$(YEAR)'

.PHONY: all help clean build build_container run minify

all: clean build run

help:  ## Show help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

clean: ## clean build
	@rm -f $(BUILD_DIR)/$(MAIN_BIN)

build: ## build binary
	@CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(MAIN_BIN) main.go

build_container: ## build container amd64
	@docker build -t nothingtodo .

run: ## run binary
	$(BUILD_DIR)/$(MAIN_BIN)

# Overwrites static/ in place, so it's gated to the Docker build (MINIFY_INPLACE=1).
minify: ## minify static (Docker build only)
	@[ "$(MINIFY_INPLACE)" = 1 ] || { echo "minify rewrites static/ in place; run via Docker build or MINIFY_INPLACE=1"; exit 1; }
	@minify static/index.html -o static/index.html
	@minify static/styles.css -o static/styles.css
