FROM golang:1.24-bookworm AS builder
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
ENV GOCACHE=/src/.cache/go-build
ENV GOMODCACHE=/src/.cache/go-mod
ENV GOTMPDIR=/src/.cache/go-tmp

COPY go.mod go.sum ./
RUN mkdir -p /src/.cache/go-build /src/.cache/go-mod /src/.cache/go-tmp
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags "-s -w -X clustercost-agent-k8s/internal/version.Version=${VERSION}" -o /out/clustercost-agent ./cmd/agent

FROM debian:bookworm AS bpf-builder
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends clang llvm bpftool make libbpf-dev && rm -rf /var/lib/apt/lists/*
WORKDIR /bpf
COPY bpf/ /bpf/
RUN if [ "${TARGETARCH}" = "amd64" ]; then ARCH=x86; elif [ "${TARGETARCH}" = "arm64" ]; then ARCH=arm64; else ARCH=${TARGETARCH}; fi; make ARCH=${ARCH} all

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=builder /out/clustercost-agent /clustercost-agent
COPY --from=bpf-builder /bpf/flows.bpf.o /opt/clustercost/bpf/flows.bpf.o
ENTRYPOINT ["/clustercost-agent"]
