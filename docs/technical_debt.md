# Technical Debt: Network Cost Precision & Hybrid Connectivity

## The Problem: "The Visibility Gap"
The current Agent architecture accurately classifies:
*   **Public Internet** (Traffic to Public IPs).
*   **Cross-AZ** (Traffic to IPs in different zones locally).
*   **Same-Zone** (Free traffic).

**The Gap**:
The Agent treats **all** Private IPs outside the local cluster (e.g., `10.50.x.x`) as "Generic Private Traffic" (bucketed as `VPCPrivate` or `CrossAZ`).
It cannot distinguish between:
1.  **Cross-Region Peering**: Traffic to a VPC in `us-east-1` (Cost: ~$0.02/GB).
2.  **Hybrid / VPN**: Traffic to On-Prem via VPN/DirectConnect (Cost: Varied).
3.  **Cross-AZ Peering**: Traffic to a VPC in the *same* region (Cost: ~$0.01/GB).

Without this distinction, real-time cost estimates for private non-local traffic may be inaccurate (usually under-estimated).

## Proposed Solution: "The Smart Agent" (Bi-Directional Sync)
To solve this without burdening the user with manual config, we propose a **Bi-Directional Control Plane**.

### Architecture
1.  **Server-Side Knowledge**:
    *   The Central Server (Backend) has AWS API Access.
    *   It queries `ec2:DescribeSubnets` to build a "Global Network Map" (CIDR $\rightarrow$ Region/Type).
2.  **Push Mechanism**:
    *   The gRPC connection becomes bidirectional (`stream ConfigUpdate`).
    *   The Server pushes the Global Network Map to the Agent.
3.  **Agent Logic**:
    *   The Agent uses this map to precisely classify `10.50.x.x` as `CrossRegion`.
    *   The Agent populates `egress_cross_region_bytes` in the report.

## Roadmap Status
*   [ ] **Phase 1 (Current)**: Backend Enrichment. Agent sends flows; Backend applies pricing logic. (5m accuracy).
*   [ ] **Phase 2 (Future)**: Add `egress_cross_region_bytes` to Proto.
*   [ ] **Phase 3 (Future)**: Implement Bi-Directional gRPC Config Stream.
