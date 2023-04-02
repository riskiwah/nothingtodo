MAIN_BIN=nothingtodo
BUILD_DIR=./dist
FLAGS=-v -ldflags="-X main.commit=`git rev-parse --short HEAD`"

.PHONY: help
help:  ## Show help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

clean: ## clean build
	@rm $(BUILD_DIR)/$(MAIN_BIN)

build: ## build binary
	@CGO_ENABLED=0 go build $(FLAGS) -o dist/nothingtodo main.go

run: ## run binary
	$(BUILD_DIR)/$(MAIN_BIN)

minify: ## minify static
	@minify static/index.html -o static/index.html
	@minify static/styles.css -o static/styles.css