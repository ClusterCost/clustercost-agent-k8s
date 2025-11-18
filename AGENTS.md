# Repository Guidelines

## Project Structure & Module Organization
`cmd/agent` contains the CLI entrypoint; keep flags and wiring here. Core logic lives in `internal/` (collectors, enricher, aggregator, API exporters, pricing utilities), and each package keeps its own `*_test.go` neighbors. Helm and manifest assets sit in `deployment/helm`, while reproducible playgrounds stay under `examples/`. Generated data such as AWS price tables go to `internal/config/aws_prices_gen.go`; run generation tools instead of editing these files directly.

## Build, Test, and Development Commands
Use `make build` (or `go build ./cmd/agent`) to create `bin/clustercost-agent-k8s` with version metadata baked into `internal/version`. `make run` runs the agent against your current kubeconfig, and `make tidy` refreshes module dependencies. CI mirrors `make test` and `make lint`, so run both locally to catch regressions before opening a PR. Pricing helpers live behind `make generate-pricing` and `make generate-pricing-all`; they regenerate embedded AWS price maps and finish with `gofmt`.

## Coding Style & Naming Conventions
This is a Go module: default to tabs for indentation, gofmt formatting, and idiomatic Go naming (packages lowercase, exported symbols in PascalCase). `golangci-lint run ./...` enforces vet, staticcheck, govet, and style rules—keep it passing. Favor structured logging via `internal/logging` helpers, and keep kube/client abstractions under `internal/kube` rather than scattering client-go calls.

## Testing Guidelines
Write unit tests alongside the code under test using `testing` plus subtests/table-driven patterns (`func TestCollector_Sync(t *testing.T)`). Add integration tests under the relevant package or `examples/` when the Kubernetes API is involved. `go test ./...` must stay clean and reasonably fast; target meaningful coverage around pricing math, allocation, and API serializers. Add fixtures or fake clients inside `internal/.../testdata` so tests remain deterministic.

## Commit & Pull Request Guidelines
Follow the existing Conventional Commits style (`feat: improve healthcheck`, `fix: change docker build`) for clear history and automated release notes. Each PR should describe the behavior change, include `go test`/`golangci-lint` results, and reference any issues or dashboards affected. Include configuration docs or screenshots when UI/metric output changes, and keep PRs scoped to one concern so reviewers can reason about the cluster impact.

## Security & Configuration Tips
Only the Kubernetes API and Metrics API should be contacted; avoid adding outbound calls. Prefer configuration via `deployment/helm/values.yaml`, `CLUSTERCOST_*` env vars, or the ConfigMap file read by `internal/config`. When touching RBAC or HTTP handlers, double-check that `/metrics` remains read-only and that `/api/*` continues to exclude sensitive node credentials. For price updates, use the `hack/cmd/generate-pricing` tool with AWS credentials scoped to pricing read access.
