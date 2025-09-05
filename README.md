# GameServer Operator — Quick Runbook (Win11 + WSL2 + Kubebuilder)

> For more details see [DESIGN-SUMMAR.md](docs/DESIGN-SUMMARY.md) and [MECHANICS.md](docs/MECHANICS.md).

## 0) Dev setup (Windows 11, VS Code, WSL2)
- Install WSL2 (Ubuntu) and Docker Desktop → enable **WSL 2 based engine** and **WSL Integration** for Ubuntu.
- In WSL:
```bash
sudo apt update && sudo apt install -y make curl git ca-certificates
# Go
wget https://go.dev/dl/go1.22.5.linux-amd64.tar.gz && sudo tar -C /usr/local -xzf go1.22.5.linux-amd64.tar.gz
echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.profile && source ~/.profile
# kubectl + kind
curl -Lo kind https://kind.sigs.k8s.io/dl/v0.23.0/kind-linux-amd64 && chmod +x kind && sudo mv kind /usr/local/bin/
curl -Lo kubectl https://dl.k8s.io/release/v1.30.4/bin/linux/amd64/kubectl && chmod +x kubectl && sudo mv kubectl /usr/local/bin/
# kubebuilder
curl -L https://go.kubebuilder.io/dl/latest/linux/amd64 | tar -xz -C /tmp && sudo mv /tmp/kubebuilder_*_linux_amd64 /usr/local/kubebuilder
echo 'export PATH=/usr/local/kubebuilder/bin:$PATH' >> ~/.profile && source ~/.profile
```
- Fix Docker-in-WSL if needed:
```bash
SOCK_GROUP=$(stat -c '%G' /var/run/docker.sock); [ "$SOCK_GROUP" = "UNKNOWN" ] && \
  sudo groupadd -g $(stat -c '%g' /var/run/docker.sock) docker || true; \
  sudo usermod -aG docker $USER && newgrp docker
```

## 1) Initialize project & CRDs
```bash
mkdir gameserver-operator && cd $_
git init
go mod init github.com/<you>/gameserver-operator
kubebuilder init --domain=example.com --repo=github.com/<you>/gameserver-operator
kubebuilder create api --group=game --version=v1alpha1 --kind=GameServer --resource --controller
kubebuilder create api --group=game --version=v1alpha1 --kind=GSDeployment --resource --controller
```
**API package tips**
- `api/v1alpha1/doc.go`:
```go
// +kubebuilder:object:generate=true
// +groupName=game.example.com
package v1alpha1
```
- `groupversion_info.go` registers GameServer, GameServerList, GSDeployment, GSDeploymentList.
- Short names:
```go
//+kubebuilder:resource:shortName=gs   // GameServer
//+kubebuilder:resource:shortName=gsd  // GSDeployment
```
**Generate & install CRDs to current cluster**
```bash
make generate
make manifests
make install
```

## 2) Controllers (design recap)
- **GameServer controller**
  - Ensures one Pod (hostNetwork: true) per GameServer; injects `GAME_PORT` from `spec.port`; readiness probe `/status`.
  - Every 10s polls `http://<hostIP>:<port>/status`; updates `.status.players/.maxPlayers/.phase/.zeroSince` + `Reachable` condition.
- **GSDeployment controller**
  - Ensures `minReplicas`; allocates unique ports from `[30000, 32000]` (configurable).
  - Scale up when **any** GS ≥ threshold (default 80%); add one up to `maxReplicas`.
  - Scale down GS idle (`players==0`) for > N sec (default 60), not below `minReplicas`.
  - Reacts to GameServer **status** updates (event-driven).

**Manager wiring (controller-runtime ≥ v0.15)**
```go
import (
  "sigs.k8s.io/controller-runtime/pkg/metrics/server"
  "sigs.k8s.io/controller-runtime/pkg/healthz"
)

mgr, _ := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
  Scheme: scheme,
  Metrics: server.Options{ BindAddress: ":8080" },
  HealthProbeBindAddress: ":8081", // or: Health: healthz.Options{BindAddress: ":8081"}
  LeaderElection: true,
  LeaderElectionID: "gameserver-operator.game.example.com",
})

utilruntime.Must(gamev1alpha1.AddToScheme(scheme))
(&controllers.GameServerReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr)
(&controllers.GSDeploymentReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr)
```

## 3) Build & install on kind
```bash
kind create cluster
make install                           # CRDs
make docker-build IMG=gameserver-operator:dev
kind load docker-image gameserver-operator:dev
make deploy IMG=gameserver-operator:dev
kubectl -n gameserver-operator-system get deploy,pods
```
**Verify image in kind**
```bash
docker exec kind-control-plane crictl images | grep gameserver-operator
# or:
kubectl run imgcheck --restart=Never --image=gameserver-operator:dev -- sleep 60
```
**Sample GSDeployment (namespace `games`)**
```yaml
apiVersion: game.example.com/v1alpha1
kind: GSDeployment
metadata: { name: fleet-a, namespace: games }
spec:
  image: kyon/gameserver:latest
  pollPath: /status
  minReplicas: 1
  maxReplicas: 5
  scaleUpThresholdPercent: 80
  scaleDownZeroSeconds: 60
  portRange: { start: 30000, end: 30010 }
```
```bash
kubectl create ns games
kubectl -n games apply -f gsd.yaml
watch -n1 'kubectl -n games get gsd,gs,pods -o wide'
```

## 4) Build, install & deploy on EKS
**Point kubectl to EKS**
```bash
aws eks update-kubeconfig --region <REGION> --name <CLUSTER>
```
**Install CRDs**
```bash
make manifests && make install
kubectl get crd | grep game.example.com
```
**Build & push operator image to ECR**
```bash
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
REGION=<REGION>
REPO=gameserver-operator
ECR_URI=$ACCOUNT_ID.dkr.ecr.$REGION.amazonaws.com/$REPO:eks

aws ecr create-repository --repository-name $REPO --region $REGION 2>/dev/null || true
aws ecr get-login-password --region $REGION | docker login --username AWS --password-stdin $ACCOUNT_ID.dkr.ecr.$REGION.amazonaws.com
docker build -t $ECR_URI .
docker push $ECR_URI
```
**Deploy operator**
```bash
make deploy IMG=$ECR_URI
kubectl -n gameserver-operator-system get deploy,pods
```
**Deploy GSDeployment**
```bash
kubectl apply -f gsd.yaml
watch -n1 'kubectl -n games get gsd,gs,pods -o wide'
```

## 5) Debugging & logs
**Verbose logs**
```bash
NS=gameserver-operator-system
kubectl -n $NS patch deploy/gameserver-operator-controller-manager --type='json' \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--zap-log-level=debug"}]'
kubectl -n $NS logs deploy/gameserver-operator-controller-manager -c manager --since=10m --timestamps
```
**Events**
```bash
kubectl -n $NS get events --sort-by=.lastTimestamp | tail -n 50
```
**RBAC checks**
```bash
SA=$(kubectl -n $NS get deploy/gameserver-operator-controller-manager -o jsonpath='{.spec.template.spec.serviceAccountName}')
kubectl auth can-i create pods -n <workload-ns> --as=system:serviceaccount:$NS:$SA
kubectl auth can-i update gameservers/status --group game.example.com -n <workload-ns> --as=system:serviceaccount:$NS:$SA
```
**Run locally**
```bash
make run   # runs manager against current kubeconfig
```

## 6) Common pitfalls (and fixes)
- Docker not available in WSL → Enable Docker Desktop WSL Integration; `wsl --shutdown`; add user to `docker` group.
- Permission denied /var/run/docker.sock → `usermod -aG docker $USER && newgrp docker`.
- kind image not found → `kind load docker-image <img>`; ensure `imagePullPolicy: IfNotPresent`.
- `MetricsBindAddress` compile error → use `Metrics: server.Options{BindAddress: ":8080"}` (and possibly `Health: healthz.Options{...}`).
- Module path mismatch in Docker build → `go.mod` `module github.com/<you>/gameserver-operator`; imports must match; Dockerfile must copy `api/`, `internal/`, `cmd/`; check `.dockerignore`.
- Deepcopy not generated → all API files `package v1alpha1`; `doc.go` markers; `groupversion_info.go` registers all kinds; `make generate` (temporary: manual DeepCopy methods file).
- “no kind registered in scheme” → add `utilruntime.Must(v1alpha1.AddToScheme(scheme))` in main.
- Controller not reconciling → ensure `cmd/main.go` registers BOTH reconcilers via `SetupWithManager`; ensure Dockerfile builds `cmd/main.go` and copies `internal/`.
- RBAC → add kubebuilder RBAC markers to allow pods/events create/update and CRD verbs; `make manifests && make install`; verify with `kubectl auth can-i ... --as system:serviceaccount:<ns>:<sa>`.
- Namespace policy (PSA) → if enforced, `hostNetwork: true` may require privileged namespace.
- Logs & events → `--zap-log-level=debug`; inspect operator logs and workload namespace events.
- Image drift → rebuild, `kind load docker-image`, `kubectl set image` and rollout restart.

## 7) Manual test pod (sanity)
```yaml
apiVersion: apps/v1
kind: Deployment
metadata: { name: gs-manual, namespace: games }
spec:
  replicas: 1
  selector: { matchLabels: { app: gs-manual } }
  template:
    metadata: { labels: { app: gs-manual } }
    spec:
      hostNetwork: true
      dnsPolicy: ClusterFirstWithHostNet
      containers:
      - name: server
        image: kyon/gameserver:latest
        env: [{ name: GAME_PORT, value: "30001" }]
        ports: [{ containerPort: 30001 }]
        readinessProbe:
          httpGet: { path: /status, port: 30001 }
          initialDelaySeconds: 2
          periodSeconds: 5
```

## 8) Daily stop/resume
- Stop: commit code; optionally `docker stop kind-control-plane`; `wsl --shutdown`; quit Docker Desktop.
- Resume: start Docker Desktop; open WSL; `docker start kind-control-plane`; `kubectl config use-context kind-kind`; verify operator/pods.
