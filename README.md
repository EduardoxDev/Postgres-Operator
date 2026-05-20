<div align="center">
  <img src="https://raw.githubusercontent.com/devicons/devicon/master/icons/postgresql/postgresql-original.svg" width="80" />
  <h1>postgres-operator</h1>
  <p>Kubernetes Operator em Go para provisionar e gerenciar clusters PostgreSQL.</p>

  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.22-00ADD8?style=flat-square&logo=go&logoColor=white" /></a>
  <a href="https://kubernetes.io"><img src="https://img.shields.io/badge/Kubernetes-1.28+-326CE5?style=flat-square&logo=kubernetes&logoColor=white" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache_2.0-22c55e?style=flat-square" /></a>
</div>

<br/>

Declare uma CR, o operator resolve o resto — `StatefulSet`, `Services`, credenciais com senha aleatória e replicação via streaming. Deletar a CR limpa tudo via `ownerReferences`.

```yaml
apiVersion: databases.example.io/v1alpha1
kind: PostgresDatabase
metadata:
  name: myapp-db
spec:
  version: "16"
  replicas: 3        # primary + 2 hot-standby replicas
  storage:
    size: 20Gi
  database: myapp
  username: myapp
```

<br/>

---

## Instalação

> **Pré-requisitos** — `go 1.22+` · `docker` · `kubectl` · `kind`

```bash
# cluster local
kind create cluster --name pg-dev --wait 60s

# build e push da imagem para o cluster
docker build -t postgres-operator:dev .
kind load docker-image postgres-operator:dev --name pg-dev

# CRD + RBAC + operator
kubectl apply -f config/crd/bases/
kubectl create namespace postgres-operator-system
kubectl apply -f config/rbac/
kubectl apply -f config/manager/manager.yaml
```

---

## Como fica na prática

Após aplicar a CR, o operator cria todos os recursos e atualiza o status em tempo real.

**Recursos criados automaticamente**

```
$ kubectl get all -l "app.kubernetes.io/instance=myapp-db"

NAME             READY   STATUS    RESTARTS   AGE
pod/myapp-db-0   1/1     Running   0          13m

NAME                        TYPE        CLUSTER-IP      PORT(S)    AGE
service/myapp-db-headless   ClusterIP   None            5432/TCP   13m
service/myapp-db-svc        ClusterIP   10.96.202.223   5432/TCP   13m

NAME                        READY   AGE
statefulset.apps/myapp-db   1/1     13m
```

**Status da CR**

```
$ kubectl get pgdb

NAME       PHASE     READY   VERSION   AGE
myapp-db   Running   1       16        60s
```

**PostgreSQL respondendo**

```
$ kubectl exec myapp-db-0 -- psql -U myapp -c "SELECT version();"

  PostgreSQL 16.14 on x86_64-pc-linux-gnu, compiled by gcc 14.2.0, 64-bit
```

---

## Credenciais

O operator gera um `Secret` com senha de 24 bytes e DSN pronto para uso.

```bash
# ver o DSN
kubectl get secret myapp-db-credentials \
  -o jsonpath="{.data.dsn}" | base64 -d

# → postgres://myapp:<senha>@myapp-db-svc.default.svc.cluster.local:5432/myapp
```

Injete direto no seu Deployment:

```yaml
env:
  - name: DATABASE_URL
    valueFrom:
      secretKeyRef:
        name: myapp-db-credentials
        key: dsn
```

> Prefere trazer sua própria senha? Use `spec.passwordSecretRef` para apontar para um `Secret` existente.

---

## Referência da spec

```yaml
spec:
  version: "16"             # "14" | "15" | "16" | "17"
  replicas: 1               # 1 = standalone  ·  2+ = HA com streaming replication
  database: appdb
  username: appuser

  storage:
    size: 10Gi
    storageClassName: fast-ssd   # opcional

  resources:                     # opcional
    requests: { cpu: 100m, memory: 256Mi }
    limits:   { cpu: "1",  memory: 512Mi }

  passwordSecretRef:             # opcional — omitir gera senha automática
    name: meu-secret
    key: password
```

**`.status` mantido em sincronia com o cluster real:**

```yaml
status:
  phase: Running              # Pending | Running | Failed
  readyReplicas: 3
  secretName: myapp-db-credentials
  serviceName: myapp-db-svc
  conditions:
    - type: Ready
      status: "True"
      reason: AllReplicasReady
      message: 3/3 replicas ready
```

---

## Alta disponibilidade

Com `replicas: 3` o operator provisiona um primário e dois hot-standbys via streaming replication.

```
pod/myapp-db-0   ─── PRIMARY      ←  escrita + leitura  (myapp-db-svc)
pod/myapp-db-1   ─── HOT STANDBY  ←  WAL stream de -0
pod/myapp-db-2   ─── HOT STANDBY  ←  WAL stream de -0
```

Cada réplica tem um DNS estável via headless service:

```
myapp-db-0.myapp-db-headless.default.svc.cluster.local
myapp-db-1.myapp-db-headless.default.svc.cluster.local
```

O init container em cada pod de réplica executa `pg_basebackup` contra o primário antes de subir o PostgreSQL, e escreve `standby.signal` para entrar em hot-standby automaticamente.

---

## Comandos úteis

```bash
# sessão psql interativa
kubectl exec -it myapp-db-0 -- psql -U myapp myapp

# escalar para HA
kubectl patch pgdb myapp-db --type=merge -p '{"spec":{"replicas":3}}'

# port-forward para testar localmente
kubectl port-forward svc/myapp-db-svc 5432:5432

# logs do operator em tempo real
kubectl -n postgres-operator-system logs -f deployment/postgres-operator
```

---

## Estrutura

```
api/v1alpha1/
  postgresdatabase_types.go       spec, status e constantes
  zz_generated.deepcopy.go        gerado pelo controller-gen

cmd/main.go                       entrypoint — registra schemes e inicia o manager

internal/controller/
  postgresdatabase_controller.go  reconciler, finalizer, status e events
  resources.go                    builders de StatefulSet e Services

config/
  crd/bases/                      CRD com schema OpenAPI v3
  rbac/                           ClusterRole, Binding, ServiceAccount
  manager/                        Deployment do operator
  samples/                        exemplos de CR prontos para aplicar
```

---

<div align="center">
  <sub>Built with <a href="https://sigs.k8s.io/controller-runtime">controller-runtime</a> · Apache 2.0</sub>
</div>
