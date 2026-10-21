GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
LDFLAGS  := -s -w

.PHONY: build test lint tidy

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/a2a-discord-gtw ./cmd/a2a-discord-gtw

test:
	go test -race ./...

lint:
	$(GOLANGCI) run

tidy:
	go mod tidy

.PHONY: run
run: build
	./bin/a2a-discord-gtw serve --data-dir ./data --log-level debug

ARCH := $(shell go env GOARCH)

.PHONY: image
image:
	rm -rf .docker-ctx && mkdir -p .docker-ctx/linux/$(ARCH)
	GOOS=linux GOARCH=$(ARCH) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o .docker-ctx/linux/$(ARCH)/a2a-discord-gtw ./cmd/a2a-discord-gtw
	cp -r docker .docker-ctx/
	docker build --platform linux/$(ARCH) -t a2a-discord-gtw:dev -f Dockerfile .docker-ctx
