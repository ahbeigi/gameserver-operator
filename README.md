# GameServer Operator — Quick Runbook (Win11 + WSL2 + Kubebuilder)
This repository contains the code and configuration for a Kubernetes operator that manages game servers and their deployments.  
It includes custom resource definitions (CRDs), controllers, and supporting manifests to handle scaling, updates, and lifecycle management.  
For more details, see [DesignSummary.md](docs/DesignSummary.md) and [RepoOverview.md](docs/RepoOverview.md).


## 1) Dev setup (Windows 11, VS Code, WSL2)
- Install WSL2 (Ubuntu) and Docker Desktop : enable **WSL 2 based engine** and **WSL Integration** for Ubuntu.
- In WSL:
```
sudo apt update && sudo apt install -y make curl git ca-certificates
# Go
wget https://go.dev/dl/go1.25.0.linux-amd64.tar.gz && sudo tar -C /usr/local -xzf go1.25.0.linux-amd64.tar.gz
echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.profile && source ~/.profile
# kubectl + kind
curl -Lo kind https://kind.sigs.k8s.io/dl/v0.30.0/kind-linux-amd64 && chmod +x kind && sudo mv kind /usr/local/bin/
curl -Lo kubectl https://dl.k8s.io/release/v1.34.0/bin/linux/amd64/kubectl && chmod +x kubectl && sudo mv kubectl /usr/local/bin/
# kubebuilder
curl -L https://go.kubebuilder.io/dl/latest/linux/amd64 | tar -xz -C /tmp && sudo mv /tmp/kubebuilder_*_linux_amd64 /usr/local/kubebuilder
echo 'export PATH=/usr/local/kubebuilder/bin:$PATH' >> ~/.profile && source ~/.profile
```
- Fix Docker-in-WSL if needed:
```
sudo usermod -aG docker $USER && newgrp docker
```

## 2) Initialize project & CRDs
```
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
```
make generate
make manifests
make install
```

## 3) Controllers (design recap)
- **GameServer controller**
  - Ensures one Pod (hostNetwork: true) per GameServer; injects `GAME_PORT` from `spec.port`; readiness probe `/status`.
  - Every 10s polls `http://<hostIP>:<port>/status`; updates `.status.players/.maxPlayers/.phase/.zeroSince` + `Reachable` condition.
- **GSDeployment controller**
  - Ensures `minReplicas`; allocates unique ports from `[30000, 32000]` (configurable).
  - Scale up when **any** GS ≥ threshold (default 80%); add one up to `maxReplicas`.
  - Scale down GS idle (`players==0`) for > N sec (default 60), not below `minReplicas`.
  - Reacts to GameServer **status** updates (event-driven).


## 4) Build & install on kind cluster
```
kind create cluster
make install                           # CRDs
make docker-build IMG=gameserver-operator:dev
kind load docker-image gameserver-operator:dev
make deploy IMG=gameserver-operator:dev
kubectl -n gameserver-operator-system get deploy,pods
```

**Sample GSDeployment (namespace `games`)**
```
apiVersion: game.example.com/v1alpha1
kind: GSDeployment
metadata:
  name: shooter-fleet
  namespace: games
spec:
  image: kyon/gameserver:latest
  pollPath: /status
  portRange: { start: 30000, end: 30005 }
  minReplicas: 2
  maxReplicas: 5
  scaleUpThresholdPercent: 80
  scaleDownZeroSeconds: 60
  updateStrategy:
    type: NoDisruption
    drainTimeoutSeconds: 7200
    maxSurge: 2
    maxUnavailable: 0
  parameters:
    maxPlayers: 32
```
Run:
```
kubectl create ns games
kubectl apply -f gsd.yaml
kubectl -n games get gsd,gs,pods -o wide
```

## 5) Build, install & deploy on EKS
**Point kubectl to EKS**
```
aws eks update-kubeconfig --region us-east-1 --name <CLUSTER>
```
**Install CRDs**
```
make manifests && make install
kubectl get crd | grep game.example.com
```
**Build & push operator image to ECR**
```
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
REGION=us-east-1
REPO=gameserver-operator
ECR_URI=$ACCOUNT_ID.dkr.ecr.$REGION.amazonaws.com/$REPO:eks

aws ecr create-repository --repository-name $REPO --region $REGION
aws ecr get-login-password --region $REGION | docker login --username AWS --password-stdin $ACCOUNT_ID.dkr.ecr.$REGION.amazonaws.com
docker build -t $ECR_URI .
docker push $ECR_URI
```
**Deploy operator**
```
make deploy IMG=$ECR_URI
kubectl -n gameserver-operator-system get deploy,pods
```
**Deploy GSDeployment**
```
kubectl apply -f gsd.yaml
kubectl -n games get gsd,gs,pods -o wide
```

## 6) Debugging & logs
**Verbose logs**
```
NS=gameserver-operator-system
kubectl -n $NS logs deploy/gameserver-operator-controller-manager -c manager --since=10m
```
**Events**
```
kubectl -n $NS get events --sort-by=.lastTimestamp | tail -n 50
```
**RBAC checks**
```
SA=$(kubectl -n $NS get deploy/gameserver-operator-controller-manager -o jsonpath='{.spec.template.spec.serviceAccountName}')
kubectl auth can-i create pods -n games --as=system:serviceaccount:$NS:$SA
kubectl auth can-i update gameservers/status -n games --as=system:serviceaccount:$NS:$SA
```
**Run locally**
```
make run   # runs manager against current kubeconfig
```

## 7) Common pitfalls (and fixes)
- Docker not available in WSL : Enable Docker Desktop WSL Integration; `wsl --shutdown`; add user to `docker` group.
- Permission denied /var/run/docker.sock : `usermod -aG docker $USER && newgrp docker`.
- Module path mismatch in Docker build : `go.mod` `module github.com/<you>/gameserver-operator`; imports must match; Dockerfile must copy `api/`, `internal/`, `cmd/`; check `.dockerignore`.
- Deepcopy not generated : all API files `package v1alpha1`; `doc.go` markers; `groupversion_info.go` registers all kinds; `make generate` (temporary: manual DeepCopy methods file).
- Error "no kind registered in scheme" : add `utilruntime.Must(v1alpha1.AddToScheme(scheme))` in main.
- Controller not reconciling : ensure `cmd/main.go` registers BOTH reconcilers via `SetupWithManager`; ensure Dockerfile builds `cmd/main.go` and copies `internal/`.
- Image drift : rebuild, `kind load docker-image`, `kubectl set image` and rollout restart.

## 8) Manual test pod (sanity)
```
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gs-manual
  namespace: games
spec:
  replicas: 1
  selector:
    matchLabels:
      app: gs-manual
  template:
    metadata:
      labels:
        app: gs-manual
    spec:
      hostNetwork: true
      dnsPolicy: ClusterFirstWithHostNet
      containers:
        - name: server
          image: kyon/gameserver:latest
          env:
            - name: GAME_PORT
              value: "30001"
          ports:
            - containerPort: 30001
          readinessProbe:
            httpGet:
              path: /status
              port: 30001
            initialDelaySeconds: 2
            periodSeconds: 5
            timeoutSeconds: 2
```