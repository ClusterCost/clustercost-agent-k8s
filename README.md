# ClusterCost Kubernetes Agent

`clustercost-agent-k8s` is the open-source Kubernetes cost visibility agent that powers the ClusterCost platform. It runs as a per-node DaemonSet, collects allocation signals (pods, namespaces, nodes, services), correlates usage metrics and eBPF network data, and exposes real-time hourly cost estimates via Prometheus metrics and a JSON API for the local dashboard.

## Highlights

- **Cluster-wide visibility** – CPU/memory requests and usage mapped to pods, namespaces, nodes, controllers, and cluster totals.
- **Label-aware allocation** – understands `team`, `service`, `env`, `client`, and `cost_center` labels across namespaces and workloads.
- **Prometheus + JSON API** – scrape `/metrics` or call `/api/cost/*` for summaries, per-namespace, or per-pod views.
- **Config-driven pricing** – supply CPU and memory hourly prices per region/provider via ConfigMap, env vars, or flags.
- **Secure-by-default** – read-only RBAC, no outbound calls, minimal resource footprint.

## Architecture Overview

```
Kubernetes API + Metrics API --> Collectors --> Enricher --> Aggregator --> Prometheus / HTTP API
```

The agent periodically performs a scrape (default 30s):

1. **Collectors** gather pods, namespaces, nodes, and usage metrics.
2. **Label Enricher** merges namespace and workload labels for consistent allocation keys.
3. **Aggregator** derives hourly cost per entity and stores the latest dataset.
4. **Exporters** project the data via `/metrics`, `/api/health`, `/api/cost/summary`, `/api/cost/namespaces`, and `/api/cost/pods`.

## Quickstart with Helm

```bash
helm repo add clustercost https://charts.clustercost.io
helm repo update
helm install clustercost-agent clustercost/clustercost-agent-k8s \
  --namespace clustercost --create-namespace \
  --set pricing.cpuHourPrice=0.046 \
  --set pricing.memoryGibHourPrice=0.005 \
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
| **Flags** | `--listen-addr`, `--scrape-interval`, `--cluster-name`, `--cpu-price`, `--memory-price`, etc. |
| **Environment** | `CLUSTER_NAME` (hard override for the displayed cluster name), `CLUSTERCOST_LOG_LEVEL`, `CLUSTERCOST_LISTEN_ADDR`, `CLUSTERCOST_SCRAPE_INTERVAL`, `CLUSTERCOST_CPU_HOUR_PRICE`, `CLUSTERCOST_MEMORY_GIB_HOUR_PRICE`, `CLUSTERCOST_PROVIDER`, `CLUSTERCOST_REGION`, `CLUSTERCOST_CLUSTER_NAME`. |
| **ConfigMap YAML** | Mounted file referenced via `CLUSTERCOST_CONFIG_FILE` or `--config`. Keys mirror the struct names in `internal/config`. |

Example ConfigMap snippet:

```yaml
pricing:
  provider: aws
  region: us-east-1
  cpuHourPrice: 0.046
  memoryGibHourPrice: 0.005
  aws:
    nodePrices:
      us-east-1:
        m5.large: 0.096
        m5.xlarge: 0.192
clusterName: prod-us-east-1
scrapeIntervalSeconds: 45
```

### Refreshing AWS node prices

The repo includes `hack/cmd/generate-pricing`, a helper that talks to the AWS Pricing API and regenerates the embedded `defaultAWSNodePrices` map. To update pricing:

```bash
# Supply the regions and instance types you care about (comma separated)
make generate-pricing \
  REGIONS="us-east-1,us-east-2,us-west-2,eu-west-1,eu-central-1,ap-southeast-1,ap-southeast-2,ap-northeast-1,ap-northeast-2" \
  INSTANCE_TYPES="m5.large,m5.xlarge,m5.2xlarge,c5.large,c5.xlarge,c5.2xlarge"

# Or let the tool discover every instance type automatically
make generate-pricing-all REGIONS="us-east-1,us-east-2,us-west-2"
```

If you want to cover *all* SKUs:

1. Use `aws pricing get-products` or `aws ec2 describe-instance-types` to dump the instance-type list for on-demand Linux shared capacity.
2. Build a comma-separated list (or store it in a file and pass `INSTANCE_TYPES="$(cat list.txt)"`).
3. Re-run `make generate-pricing` with those inputs. The command only needs AWS network access during generation—there are still **no runtime calls** from the agent.

## Prometheus Metrics

Key metric families exported:

- `clustercost_pod_cost_hourly{namespace,pod,node,team,service,env,client,cluster_name}`
- `clustercost_namespace_cost_hourly{namespace,team,service,env,client,cluster_name}`
- `clustercost_node_cost_hourly{node,cluster_name}`
- `clustercost_cluster_cost_hourly{cluster_name}`

Additional internal gauges capture usage and owner metadata, enabling dashboards, alerting, or recording rules.

## HTTP JSON API

- `GET /api/health` – readiness + cluster summary.
- `GET /api/cost/summary` – cluster totals with provider/region metadata, label allocation, and cost by instance type.
- `GET /api/cost/namespaces` – namespaces with hourly cost and request totals.
- `GET /api/cost/pods` – pods enriched with node placement and controller fields.
- `GET /api/cost/nodes` – node-level pricing, allocation, and utilization (raw vs allocated cost, CPU/memory usage).
- `GET /api/cost/workloads` – aggregates pods into workloads (Deployments/StatefulSets/etc.) with replica counts and cost.
- `GET /agent/v1/readyz` – readiness probe for Kubernetes; returns 200 once a snapshot is available.

## Security & RBAC

The chart provisions a dedicated ServiceAccount with:

- `get`, `list`, `watch` on pods, namespaces, deployments, services, and nodes.
- `get`, `list` on metrics.k8s.io resources.
- No write operations, no exec, and no permissions outside the cluster.

Only cluster-local APIs are contacted; there are **no outbound network calls**. TLS termination is left to the cluster ingress stack if required.

## Pricing Model

The MVP relies on static pricing numbers supplied via configuration. The default is tuned for generic AWS on-demand nodes. Override CPU and memory prices per cluster to match your blend (e.g., spot, savings plans). Future versions can plug into the ClusterCost Cloud for live price feeds.

## Roadmap

- Deeper CSV/Parquet export for offline analysis.
- Built-in rightsizing signals.
- Automatic AWS pricing sync and EKS/Fargate differentiation.
- Optional WASM policy hooks for custom allocation logic.
- Extended APIs for services/deployments and label taxonomies.

## Local Development

```bash
# Build binary
make build # or go build ./cmd/agent

# Run locally against current kube context
CLUSTERCOST_KUBECONFIG=$HOME/.kube/config \
CLUSTERCOST_CPU_HOUR_PRICE=0.04 \
CLUSTERCOST_MEMORY_GIB_HOUR_PRICE=0.004 \
go run ./cmd/agent
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
- `CLUSTERCOST_EBPF_METRICS_*` and `CLUSTERCOST_EBPF_NET_*` for object + map paths.
- `CLUSTERCOST_NETWORK_ENABLED` and `CLUSTERCOST_EBPF_METRICS_ENABLED` to enable collectors.

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
