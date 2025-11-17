FROM golang:1.24-bookworm AS builder
ARG VERSION=dev
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X clustercost-agent-k8s/internal/version.Version=${VERSION}" -o /out/clustercost-agent ./cmd/agent

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/clustercost-agent /clustercost-agent

USER nonroot
ENTRYPOINT ["/clustercost-agent"]
