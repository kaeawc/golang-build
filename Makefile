.PHONY: build test vet lint fmt complexity security licenses tidy ci clean release-check validate-workflows all

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -s -w -X main.version=$(VERSION)
BIN ?= server

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/server/

test:
	go test ./... -count=1

vet:
	go vet ./...

fmt:
	gofmt -s -w .

lint:
	golangci-lint run

complexity:
	gocyclo -over 10 -ignore '_test\.go$$' .

security:
	gosec ./...

licenses:
	go-licenses report ./...

tidy:
	go mod tidy

ci: vet test complexity lint security licenses

release-check:
	bash scripts/release-check.sh

validate-workflows:
	bash scripts/validate-workflows.sh

clean:
	rm -f $(BIN) junit-report.xml gosec-report.xml

all: build vet test
