# ClusterCost Kubernetes Agent

`clustercost-agent-k8s` is the open-source Kubernetes usage visibility agent that powers the ClusterCost platform. It runs as a per-node DaemonSet, collects allocation signals (pods, namespaces, nodes, services), correlates usage metrics and eBPF network data, and exposes real-time usage via Prometheus metrics and a readiness endpoint.

## Highlights

- **Cluster-wide visibility** – CPU/memory requests and usage mapped to pods, namespaces, nodes, controllers, and cluster totals.
- **Label-aware allocation** – understands `team`, `service`, `env`, `client`, and `cost_center` labels across namespaces and workloads.
- **Prometheus + health endpoint** – scrape `/metrics` and use `/agent/v1/readyz` for readiness.
- **Secure-by-default** – read-only RBAC, no outbound calls, minimal resource footprint.

## Architecture Overview

```
Kubernetes API --> Collectors --> Enricher --> Aggregator --> Prometheus / HTTP API
```

The agent periodically performs a scrape (default 30s):

1. **Collectors** gather pods, namespaces, nodes, and usage metrics.
2. **Label Enricher** merges namespace and workload labels for consistent allocation keys.
3. **Aggregator** stores the latest dataset.
4. **Exporters** project the data via `/metrics` and `/agent/v1/readyz`.

## Quickstart with Helm

```bash
helm repo add clustercost https://charts.clustercost.io
helm repo update
helm install clustercost-agent clustercost/clustercost-agent-k8s \
  --namespace clustercost --create-namespace \
  --set clusterName=my-prod-cluster
```

The agent will try to auto-detect the cluster name, but setting `--set clusterName=...` (or the `CLUSTER_NAME` env var) gives you exact control with zero guesswork.

For local development, use the bundled chart:

```bash
helm install clustercost-agent deployment/helm \
  --namespace clustercost --create-namespace
```

See `deployment/helm/README.md` for chart-specific configuration guidance and the full values reference.

## Configuration

| Source | Notes |
| --- | --- |
| **Flags** | `--listen-addr`, `--scrape-interval`, `--cluster-name`, `--enable-network`, `--enable-metrics`, `--cgroup-root`, `--ebpf-net-object`, `--ebpf-net-cgroup-path` |
| **Environment** | `CLUSTER_NAME` (hard override for the displayed cluster name), `CLUSTERCOST_CONFIG_FILE` |
| **ConfigMap YAML** | Mounted file referenced via `CLUSTERCOST_CONFIG_FILE` or `--config`. Keys mirror the struct names in `internal/config`. |

Example ConfigMap snippet:

```yaml
clusterName: prod-us-east-1
scrapeIntervalSeconds: 45
network:
  enabled: true
  detailed: true
metrics:
  enabled: true
  cgroupRoot: /sys/fs/cgroup
```

## Prometheus Metrics

Key metric families exported focus on usage and allocation. See the `/metrics` endpoint for the current series.

## Health

- `GET /agent/v1/readyz` – readiness probe for Kubernetes; returns 200 once a snapshot is available.

## Security & RBAC

The chart provisions a dedicated ServiceAccount with:

- `get`, `list`, `watch` on pods, namespaces, deployments, services, and nodes.
- No Metrics API dependencies (cgroup-based CPU/memory).
- No write operations, no exec, and no permissions outside the cluster.

Only cluster-local APIs are contacted; there are **no outbound network calls**. TLS termination is left to the cluster ingress stack if required.

## Roadmap

- Deeper CSV/Parquet export for offline analysis.
- Built-in rightsizing signals.
- Optional WASM policy hooks for custom allocation logic.
- Extended APIs for services/deployments and label taxonomies.

## Local Development

```bash
# Build binary
make build # or go build ./cmd/agent

# Run locally against current kube context
go run ./cmd/agent --kubeconfig "$HOME/.kube/config"
```

See `examples/daemonset` for a node-level deployment example.

## Container Image

Build a minimal distroless-based image using the multi-stage `Dockerfile`. The build includes the eBPF object files at `/opt/clustercost/bpf/`:

```bash
docker build -t ghcr.io/you/clustercost-agent-k8s:dev .
docker run --rm -p 8080:8080 \
  -e CLUSTERCOST_CLUSTER_NAME=dev-cluster \
  ghcr.io/you/clustercost-agent-k8s:dev
```

Note: eBPF requires Linux with cgroup v2 and BTF; it will not run on macOS.

## Kubernetes DaemonSet

Deploy one agent per node with host mounts and privileges:

```bash
kubectl apply -f examples/daemonset/agent.yaml
```

The DaemonSet mounts `/sys/fs/bpf`, `/sys/fs/cgroup`, and `/sys/kernel/btf` and sets:

- `NODE_NAME` for node scoping.
- `CLUSTER_NAME` for optional cluster name override.

### Remote forwarding

Configure the central ingest endpoint and optional auth:

- `CLUSTERCOST_REMOTE_ENABLED=true`
- `CLUSTERCOST_REMOTE_ENDPOINT=https://<central-agent-host>/ingest`
- `CLUSTERCOST_REMOTE_AUTH_TOKEN=...` (optional)
- `CLUSTERCOST_REMOTE_TIMEOUT=5s` (optional)

GitHub Actions builds and publishes multi-architecture (amd64 + arm64) images to Docker Hub via `.github/workflows/docker.yml`. Configure the repository secrets `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` with push access to your Docker Hub namespace before triggering the workflow.

### Release workflow

Trigger `.github/workflows/release.yml` (workflow_dispatch) to cut a GitHub Release on demand:

1. Open the "Manual Release" workflow in GitHub Actions.
2. Provide a semantic version tag (e.g., `v0.2.0`).
3. The workflow runs `go test ./...` and, if successful, creates a GitHub Release with auto-generated release notes that summarize commit history since the previous tag.

Publishing a release automatically triggers the Docker workflow (via the tag), producing multi-arch container images for that version.

## CI & Quality Gates

Every push/PR runs `.github/workflows/ci.yml`, which executes:

- `go test ./...`
- `golangci-lint` (formatting, vet, staticcheck, etc.) using `.golangci.yml`
- `gosec` for static security scanning

Fix issues locally via `make test` and `make lint` before pushing to keep CI green.
