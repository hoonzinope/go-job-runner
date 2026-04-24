BINARY_NAME := job-runner
BUILD_DIR := bin
MAIN_PKG := ./cmd
IMAGE_NAME ?= job-runner:latest
CONFIG_FILE ?= config.yml
DATA_DIR ?= data
GO ?= go

.PHONY: all build test run clean docker-build docker-run

all: build

build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w" -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PKG)

test:
	$(GO) test ./...

run: build
	./$(BUILD_DIR)/$(BINARY_NAME)

clean:
	rm -rf $(BUILD_DIR)

docker-build:
	docker build -t $(IMAGE_NAME) .

docker-run:
	docker run --rm \
		-p 8888:8888 \
		-v $(PWD)/$(CONFIG_FILE):/app/config.yml:ro \
		-v $(PWD)/$(DATA_DIR):/app/data \
		-v /var/run/docker.sock:/var/run/docker.sock \
		$(IMAGE_NAME)
