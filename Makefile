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
	@echo "Generated protobuf code in internal/proto/"

# Download dependencies (must run after proto)
deps:
	go mod tidy

# Build binaries
build:
	go build -o bin/snmpproxyd ./cmd/snmpproxyd
	go build -o bin/snmpctl ./cmd/snmpctl
	@echo "Built bin/snmpproxyd and bin/snmpctl"

clean:
	rm -rf bin/ internal/proto/*.pb.go

test:
	go test -v ./...

# Generate self-signed TLS certificates
gen-certs:
	@mkdir -p certs
	openssl req -x509 -newkey rsa:4096 \
		-keyout certs/server.key -out certs/server.crt \
		-days 365 -nodes -subj "/CN=localhost"
	@echo "Generated certs/server.{crt,key}"
