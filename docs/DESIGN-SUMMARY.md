# Brief Design Summary

## Port Allocation Strategy
- **Source of truth:** `GSDeployment.spec.portRange {start,end}` defines allowed host ports (guidance: 30000–32000).
- **Allocator scope:** Per-`GSDeployment` pool; ports treated as **globally unique** (conservative for `hostNetwork: true`) to avoid collisions even on multi-node.
- **Allocation:** On scale-up, pick the **lowest free** port not used by any live child; persist it in `GameServer.spec.port`.
- **Release & recovery:** On scale-down/deletion, return the port to the pool. On controller restart, rebuild state by listing current children and reading their `spec.port`.
- **Guardrails:** If no ports remain, set a `PortExhausted` condition on the `GSDeployment` and halt scale-up.

## When & How We Scale
- **Scale up:** If **any** `GameServer` reaches **≥ `scaleUpThresholdPercent`** utilization (`players/maxPlayers * 100`), create **one** additional `GameServer` (respect `maxReplicas` and port availability).
- **Scale down:** If `players == 0` for **> `scaleDownZeroSeconds`**, mark as idle and delete the **oldest idle** server, never going below `minReplicas`.
- **Bounds & stability:** Always enforce `minReplicas ≤ replicas ≤ maxReplicas`; apply light debounce to avoid flapping.
- **Reactivity:** Event-driven from `GameServer` **status** updates (no periodic timer in `GSDeployment`).

## Reconciliation Flow

### GameServer Controller
1. **Fetch & existence:** On `GameServer` create/update/delete (and owned Pod events), load the object; return if deleted.
2. **Ensure Pod (1:1):** If missing, create a Pod with:
   - `hostNetwork: true`, `dnsPolicy: ClusterFirstWithHostNet`
   - Image = `spec.image`
   - `env: GAME_PORT = spec.port`
   - `ports.containerPort = spec.port`
   - Readiness probe `GET /status` on `spec.port`
   - OwnerReference → the `GameServer` (for GC)
3. **Status updates:** When Pod is running, **poll** `http://<Pod.status.hostIP>:<spec.port>/status` every **10s**:
   - Update `status.players`, `status.maxPlayers`, `status.phase`, `status.endpoint`, `status.nodeName`, `status.lastPolled`
   - Manage `status.zeroSince` when `players==0`; set/clear a `Reachable` condition
   - Handle connection failures gracefully and `RequeueAfter: 10s`
4. **Cleanup:** Deleting the `GameServer` cascades to its Pod via OwnerReference.

### GSDeployment Controller
1. **Watch:** Reconcile on `GSDeployment` changes **and** on child `GameServer` **status** changes.
2. **Desired state:** List children; compute utilization and idleness:
   - Ensure **`minReplicas`**: allocate a port and create new `GameServer` objects as needed.
   - **Scale up:** if any child ≥ threshold, add **one** (respect `maxReplicas` and ports).
   - **Scale down:** delete **oldest idle** servers (players==0 for the configured window), not below `minReplicas`.
3. **Status:** Update `GSDeployment.status` (replicas, readyReplicas, conditions like `PortExhausted`, optional `allocatedPorts`).
4. **Idempotency:** Actions are derived from observed state; safe to retry and converge.

## Assumptions
- Mock server requires `hostNetwork: true` and `GAME_PORT`; the controller injects both.
- Port range is configurable per `GSDeployment`; default guidance uses 30000–32000.
- Scope focuses on core functionality (correct patterns, status loops, autoscaling); production hardening (drain, PDBs, metrics) is out of scope.
