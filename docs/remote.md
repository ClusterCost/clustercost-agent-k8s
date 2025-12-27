Central Agent Ingest

Agents POST JSON to the central endpoint. The payload schema:

{
  "clusterId": "cluster-1",
  "clusterName": "prod",
  "nodeName": "ip-10-0-1-2",
  "version": "v0.2.0",
  "timestamp": "2025-01-01T00:00:00Z",
  "snapshot": { ... }
}

The snapshot field matches the local agent snapshot (namespaces, nodes, resources,
network, and connection graphs). The central agent should aggregate by clusterId
and nodeName.

Auth
- Optional Bearer token via Authorization header.

Batching & retries

Agents spool reports to disk and POST batches:

{
  "reports": [ ... ]
}

Config:
- CLUSTERCOST_REMOTE_QUEUE_DIR (default /var/lib/clustercost/queue)
- CLUSTERCOST_REMOTE_FLUSH_EVERY (default 5s)
- CLUSTERCOST_REMOTE_MAX_BATCH (default 50)
- CLUSTERCOST_REMOTE_MAX_RETRIES (default 5)
- CLUSTERCOST_REMOTE_BACKOFF (default 10s)
- CLUSTERCOST_REMOTE_MAX_BATCH_BYTES (default 512k)
- CLUSTERCOST_REMOTE_MEMORY_BUFFER (default 200)
- CLUSTERCOST_REMOTE_GZIP (default true)

Backoff is exponential per retry (base * 2^retries).
