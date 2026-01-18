# ClusterCost Agent

The lightweight, node-local agent that powers [ClusterCost](https://clustercost.com). It sits on every node in your cluster, watches what's happening (via K8s API + eBPF), and reports usage metrics back to your dashboard.

No sidecars, no instrumentation, and read-only permissions.

## What it does

Instead of just looking at K8s requests/limits, we dig deeper:
*   **Real Usage**: CPU/Memory from cgroups (what's actually being used vs requested).
*   **Network Intelligence**: Uses eBPF to track exactly where bytes are going (Internet? Cross-AZ? Internal?) without a mesh.
*   **Node Overhead**: Separates "System" cost (kubelet, OS) from your actual Workload cost.
*   **Allocation**: Maps everything back to your custom labels (`team`, `cost_center`, `env`).

## Architecture

It's a DaemonSet. One pod per node.
1.  **Metrics Loop (15s)**: Grabs CPU/RAM stats.
2.  **Network Loop (5m)**: Aggregates network flows from the kernel.
3.  **Push**: Sends data via gRPC to your ClusterCost Dashboard.

## Quickstart

Add the repo and install the chart.

```bash
helm repo add clustercost https://charts.clustercost.com
helm repo update

# Install the agent
helm install agent clustercost/clustercost-agent-k8s \
  --namespace clustercost-agent --create-namespace \
  --set clusterName=<your-cluster-name>
```

## Configuration

You can configure the agent primarily via Environment Variables (great for GitOps) or Helm values.

### Core Settings

| Var | Default | Description |
| --- | --- | --- |
| `CLUSTER_NAME` | (Auto) | Logical name for this cluster in the dashboard. |
| `CLUSTERCOST_SCRAPE_INTERVAL` | `15s` | How often to collect CPU/Mem samples. |
| `CLUSTERCOST_ENABLE_NETWORK` | `true` | Turn eBPF network tracking on/off. |
| `CLUSTERCOST_REMOTE_ENDPOINT` | `localhost:9091` | Where to send data (gRPC). |

## Development

Prereqs: Go 1.22+, Docker, Minikube/Kind.

```bash
# Build the binary
make build

# Run tests
make test
```

**Note on eBPF**: You need a Linux environment to build/run the eBPF collectors. On Mac, use the Docker-based build.
