BINARY ?= bin/clustercost-agent-k8s
VERSION ?= dev
REGIONS ?= us-east-1,us-east-2,us-west-2,eu-west-1,eu-central-1
INSTANCE_TYPES ?= m5.large,m5.xlarge,m5.2xlarge
LDFLAGS ?= -s -w -X clustercost-agent-k8s/internal/version.Version=$(VERSION)

.PHONY: build run lint test tidy generate-pricing generate-pricing-all

build:
	@mkdir -p $(dir $(BINARY))
	GO111MODULE=on go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/agent

run:
	go run ./cmd/agent

lint:
	golangci-lint run ./...

test:
	go test ./...

tidy:
	go mod tidy

generate-pricing:
	go run ./hack/cmd/generate-pricing -regions "$(REGIONS)" -instance-types "$(INSTANCE_TYPES)" -output internal/config/aws_prices_gen.go
	gofmt -w internal/config/aws_prices_gen.go

generate-pricing-all:
	go run ./hack/cmd/generate-pricing -regions "$(REGIONS)" -all-instance-types -output internal/config/aws_prices_gen.go
	gofmt -w internal/config/aws_prices_gen.go
