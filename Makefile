.PHONY: all proto build clean test install-tools gen-certs deps

all: proto deps build

# Install protoc-gen-go
install-tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

# Generate protobuf code
proto:
	@mkdir -p internal/proto
	protoc --proto_path=proto \
		--go_out=internal/proto --go_opt=paths=source_relative \
		proto/snmpproxy.proto
	@echo "Generated protobuf code"

# Download dependencies
deps:
	go mod tidy

# Build binaries
build:
	go build -ldflags "-X main.Version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
		-o bin/snmpproxyd ./cmd/snmpproxyd
	go build -o bin/snmpctl ./cmd/snmpctl
	@echo "Built bin/snmpproxyd and bin/snmpctl"

clean:
	rm -rf bin/ internal/proto/*.pb.go coverage.out coverage.html

test:
	go test -v ./...

test-short:
	go test -v -short ./...

test-cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Generate TLS certificates
gen-certs:
	@mkdir -p certs
	openssl req -x509 -newkey rsa:4096 \
		-keyout certs/server.key -out certs/server.crt \
		-days 365 -nodes -subj "/CN=localhost"
	@echo "Generated certs/server.{crt,key}"

# Generate encryption key for secrets
gen-secret-key:
	openssl rand -out secret.key 32
	@chmod 600 secret.key
	@echo "Generated secret.key"

# Development
run:
	go run ./cmd/snmpproxyd -config config.yaml

cli:
	go run ./cmd/snmpctl -server localhost:9161 -tls-skip-verify

fmt:
	go fmt ./...

# Release builds
release:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-ldflags "-s -w -X main.Version=$$(git describe --tags --always)" \
		-o bin/snmpproxyd-linux-amd64 ./cmd/snmpproxyd
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build \
		-ldflags "-s -w -X main.Version=$$(git describe --tags --always)" \
		-o bin/snmpproxyd-darwin-amd64 ./cmd/snmpproxyd
	@echo "Built release binaries"

help:
	@echo "Targets:"
	@echo "  all          - Generate proto, deps, build"
	@echo "  proto        - Generate protobuf code"
	@echo "  build        - Build binaries"
	@echo "  test         - Run tests"
	@echo "  test-cover   - Run tests with coverage"
	@echo "  gen-certs    - Generate TLS certificates"
	@echo "  gen-secret-key - Generate encryption key"
	@echo "  run          - Run server"
	@echo "  cli          - Run CLI client"
	@echo "  clean        - Remove build artifacts"
	@echo "  release      - Build release binaries"
