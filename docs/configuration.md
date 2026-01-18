# ClusterCost Agent Configuration

The agent can be configured via Environment Variables. This is the recommended method for Kubernetes deployments (Helm Charts).

## Identity Variables (Required)
These variables tell the agent "Who it is". They must be populated via the Kubernetes Downward API.

| Variable | Description | Value `valueFrom` |
| :--- | :--- | :--- |
| `NODE_NAME` | Name of the K8s Node where this agent is running. | `spec.nodeName` |
| `POD_NAME` | Name of this Agent Pod. | `metadata.name` |
| `POD_NAMESPACE` | Namespace of this Agent Pod. | `metadata.namespace` |
| `CLUSTER_NAME` | Friendly name of the cluster. | `value: "my-cluster"` (Manual) |

## Operational Variables (Optional)
Controls the behavior of the agent.

| Variable | Default | Description |
| :--- | :--- | :--- |
| `CLUSTERCOST_SCRAPE_INTERVAL` | `15` | How often (seconds) to collect CPU/Memory metrics and send `ReportMetrics`. Min: 5s. |
| `CLUSTERCOST_ENABLE_NETWORK` | `true` | Enable/Disable eBPF network collection. |
| `CLUSTERCOST_NETWORK_REPORT_INTERVAL` | `300` | How often (seconds) to flush accumulated network flows (`ReportNetwork`). Min: 60s. |
| `CLUSTERCOST_REMOTE_ENDPOINT` | `...:9091` | Address of the ClusterCost Dashboard/Collector. |
| `CLUSTERCOST_REMOTE_PROTOCOL` | `grpc` | Protocol to use (`grpc` or `http`). |
| `CLUSTERCOST_REMOTE_AUTH_TOKEN` | `""` | Bearer token for authentication (if required). |
| `GOGC` | `100` | Go Garbage Collector target (standard Go env var). |
| `LOG_LEVEL` | `info` | Logging verbosity (`debug`, `info`, `warn`, `error`). |

## Example Helm Configuration
```yaml
env:
  - name: CLUSTER_NAME
    value: "prod-us-east-1"
  - name: CLUSTERCOST_SCRAPE_INTERVAL
    value: "15"
  - name: CLUSTERCOST_REMOTE_ENDPOINT
    value: "clustercost-dashboard.clustercost.svc.cluster.local:9091"
  - name: NODE_NAME
    valueFrom:
      fieldRef:
        fieldPath: spec.nodeName
```
