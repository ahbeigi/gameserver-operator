# Brief Design Summary

## Port Allocation Strategy
- **Source of truth:** 
`GSDeployment.spec.portRange {start,end}` defines allowed host ports.  
This range will be respected even during the surge (by applying condition `total < gsd.Spec.MaxReplicas` in the surge loop.)

- **Allocation:** 
Funtion [allocatePort()](https://github.com/ahbeigi/gameserver-operator/blob/main/internal/controller/gsdeployment_controller.go#311) is responsible to find the first free port in the portRange. This function is being called every time Reconciler creates a new GameServer object.
This port then will be listed in `GSDeployment.status.allocatedPorts` as reserved.

- **Release & recovery:** On scale-down/deletion, return the port to the pool.

## How We Scale
### Scale up
1) every 10s GameServer's Reconcile() runs and polls the pod's `/status` endpoint to get players number. Sample response:
```
{"players":0,"maxPlayers":20}
```

2) `GameServer.status.players` changes, and this will trigger GSDeployment's Reconcile() function.

3) GSDeployment's Reconcile() checks if any children is overloaded (by checking `gs.Status.Players*100/mp >= gsd.Spec.ScaleUpThresholdPercent`).

4) If so, add exactly one GameServer to the pool and update the GSDeployment object.

### Scale Down
1) GameServer's Reconcile() pool `/status` every 1-s, and if "Players==0", it sets `GameServer.status.zeroSince` to the current timestamp.

2) Change in a GS's Status triggers the parent's (GSDeployment's) Reconcile().

3) Scaledown logic in Reconcile() looks at `gs.Status.ZeroSince` and it it is older than `GSDeployment.spec.scaleDownZeroSeconds` it will add the the GS to idle list.

**Note:** The concept of "Draining" has been added to support GitOps requirement for safe rollout. See details in [gameserver-gitops repository](https://github.com/ahbeigi/gameserver-gitops).

**Note:** Min and Max replicas as well as port ranges will be respected for any scale operation.

## Reconciliation Flow

### GameServer Controller
#### Triggers on:
1) SetupWithManager() sets up watches for a given Gameserver, who owns a Pod. so every add/delete/update regarding these objects (including changes in Status) will trigger Reconcile().
2) Requeue every 10s (but returning `ctrl.Result{RequeueAfter: 10 * time.Second}` from Reconcile()'s previous run)
#### What Reconcile() does:
1) Make sure a pod exists for the Gameserver, and if not will create one.
2) Update `GameServer.status` based on polled metrics from its pod. including Phase, Players, etc.

### GSDeployment Controller
#### Triggers on:
1) SetupWithManager() sets up watches for a given GSDeployment, who owns a pool of GameServers. so every add/delete/update regarding these objects (including changes in Status) will trigger Reconcile().

#### What Reconcile() does:
1) Fetch the `GSDeployment` object and apply default values (image, update strategy, etc.).
2) List all child `GameServer` objects for this deployment and mark them as:
   - Desired: match the current spec (image, MAX_PLAYERS, etc.)
   - Outdated: differ from the current spec
3) Mark outdated servers as *draining* annotation (used by deployment strategy).
4) **Surge:** if outdated servers exist and capacity allows (`total < maxReplicas` and within `MaxSurge`), create new desired `GameServer` instances on a free port.
5) **Ensure minimum replicas:** if the current count is below `minReplicas`, create new servers until the floor is met (subject to available ports).
6) **Scale up:** if any running server is overloaded (players/maxPlayers ≥ `ScaleUpThresholdPercent`) and total is still below `maxReplicas`, add one more `GameServer`.
7) **Scale down:** if above `minReplicas`, delete idle servers:
8) Update the parent `GSDeployment.status` with replica counts, ready count, and the list of allocated ports.
9) Exit. The controller relies on event-driven triggers (spec changes, child status changes, creates/deletes) for the next reconciliation; it does not requeue on a timer.

