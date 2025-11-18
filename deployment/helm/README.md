# ClusterCost Agent Helm Chart

This chart deploys the ClusterCost Kubernetes cost agent as a single cluster-wide Deployment.

## Installation

```bash
helm repo add clustercost https://charts.clustercost.io
helm repo update
helm install clustercost-agent clustercost/clustercost-agent-k8s \
  --namespace clustercost --create-namespace
```

For local changes, run from the repo root:

```bash
helm install clustercost-agent ./deployment/helm \
  --namespace clustercost --create-namespace
```

## Key values

| Value | Description | Default |
| --- | --- | --- |
| `image.repository` | Container image | `ghcr.io/clustercost/clustercost-agent-k8s` |
| `image.tag` | Image tag. Defaults to chart appVersion when empty | `""` |
| `imagePullSecrets` | List of pull secrets | `[]` |
| `replicaCount` | Number of pods (should be 1) | `1` |
| `resources` | Pod resource requests/limits | see `values.yaml` |
| `nodeSelector`/`affinity`/`tolerations` | Scheduling hints | `{}` |
| `priorityClassName` | Assign Pod priority | `""` |
| `podSecurityContext` / `securityContext` | Pod/container security context | `{}` |
| `extraEnv` / `extraEnvFrom` | Additional environment variables | `[]` |
| `service.port` | HTTP/metrics port | `8080` |
| `service.annotations` | Extra annotations for the Service | `{}` |
| `clusterName` | Name advertised on the API/metrics (`CLUSTER_NAME` env) | `kubernetes` |
| `pricing.*` | CPU/memory pricing + optional AWS node prices overrides | see `values.yaml` |
| `config.existingConfigMap` | Use an externally managed ConfigMap for `config.yaml` | `""` |
| `metricsServiceMonitor.enabled` | Create ServiceMonitor for Prometheus Operator | `false` |
| `metricsServiceMonitor.additionalLabels` | Extra labels for ServiceMonitor selector | `{}` |
| `rbac.create` | Manage RBAC objects | `true` |

### AWS node price overrides

The agent ships with embedded AWS pricing data. To override/add entries via Helm, specify:

```yaml
pricing:
  awsNodePrices:
    us-east-1:
      m7g.large: 0.085
```

### Using an existing ConfigMap

If you manage `config.yaml` manually (e.g., via Flux or another system), set:

```yaml
config:
  existingConfigMap: my-agent-config
```

The chart will skip creating its own ConfigMap and mount the provided one.

## Upgrading

Review `values.yaml` and this README for new fields when upgrading; Helm will restart the Deployment when config changes.
### Cluster name

Set `clusterName` (or add `CLUSTER_NAME` to `extraEnv`) if you want to pin the display name; otherwise the agent will fall back to auto detection/`unknown`.
