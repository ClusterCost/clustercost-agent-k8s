BINARY ?= bin/clustercost-agent-k8s
VERSION ?= dev
REGIONS ?= us-east-1,us-east-2,us-west-2,eu-west-1,eu-central-1
INSTANCE_TYPES ?= m5.large,m5.xlarge,m5.2xlarge
LDFLAGS ?= -s -w -X clustercost-agent-k8s/internal/version.Version=$(VERSION)

.PHONY: build run lint test tidy sec

build:
	@mkdir -p $(dir $(BINARY))
	GO111MODULE=on go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/agent

run:
	go run ./cmd/agent

lint:
	@mkdir -p .cache/golangci-lint .cache/go-build
	GOLANGCI_LINT_CACHE=$(PWD)/.cache/golangci-lint GOCACHE=$(PWD)/.cache/go-build golangci-lint run --config .golangci.yml

test:
	go test ./...

test-bpf:
	docker run --privileged --rm -v $(PWD):/app -w /app golang:1.24 go test -v ./internal/ebpf/ -run TestMountBPFFS

tidy:
	go mod tidy

proto:
	protoc -I=proto --go_out=internal/proto/agent/v1 --go_opt=paths=source_relative --go-grpc_out=internal/proto/agent/v1 --go-grpc_opt=paths=source_relative proto/agent.proto

upload-latest:
	docker buildx build --platform linux/amd64,linux/arm64 -t jesuspaz/clustercost-agent-k8s:latest --push --build-arg VERSION=$(VERSION) .

sec:
	@which gosec > /dev/null || (echo "Installing gosec..." && go install github.com/securego/gosec/v2/cmd/gosec@latest)
	gosec -exclude-dir=internal/proto ./...
