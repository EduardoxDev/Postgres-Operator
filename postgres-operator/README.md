# postgres-operator

[![Go](https://img.shields.io/badge/Go-1.22-00ADD8?style=flat-square&logo=go&logoColor=white)](https://golang.org)
[![Kubernetes](https://img.shields.io/badge/Kubernetes-1.28+-326CE5?style=flat-square&logo=kubernetes&logoColor=white)](https://kubernetes.io)
[![License](https://img.shields.io/badge/License-Apache_2.0-green?style=flat-square)](LICENSE)

Kubernetes Operator em Go para gerenciar instâncias PostgreSQL. Declare o que quer; o operator cuida do resto.

```yaml
apiVersion: databases.example.io/v1alpha1
kind: PostgresDatabase
metadata:
  name: myapp-db
spec:
  version: "16"
  replicas: 3       # 1 primary + 2 read replicas
  storage:
    size: 20Gi
```

---

## Como funciona

O operator assiste objetos `PostgresDatabase` e mantém esses recursos sincronizados:

```
PostgresDatabase (myapp-db)
 ├── Secret          myapp-db-credentials   ← senha gerada automaticamente + DSN
 ├── StatefulSet     myapp-db               ← pods PostgreSQL
 ├── Service         myapp-db-svc           ← acesso ao primário
 └── Service         myapp-db-headless      ← DNS estável por pod
```

Com `replicas > 1`, cada pod não-primário roda `pg_basebackup` via init container e entra em hot-standby. O primário é sempre o pod de ordinal `0`.

Todos os recursos são filhos da CR via `ownerReferences` — deletar a CR limpa tudo automaticamente.

---

## Quickstart

**Pré-requisitos:** `go 1.22+`, `docker`, `kubectl`, `kind`

```bash
# 1. cluster local
kind create cluster --name pg-dev --wait 60s

# 2. build e load da imagem
docker build -t postgres-operator:dev .
kind load docker-image postgres-operator:dev --name pg-dev

# 3. instalar CRD e fazer deploy do operator
kubectl apply -f config/crd/bases/
kubectl create namespace postgres-operator-system
kubectl apply -f config/rbac/
kubectl apply -f config/manager/manager.yaml

# 4. criar uma instância
kubectl apply -f config/samples/v1alpha1_postgresdatabase.yaml
```

---

## Demo

Operador iniciando e adquirindo o lease de lider:

```
2026-05-20T21:56:45Z  INFO  starting postgres-operator
2026-05-20T21:56:45Z  INFO  leaderelection  successfully acquired lease
2026-05-20T21:56:46Z  INFO  Starting workers  {"controller": "postgresdatabase", "worker count": 1}
```

Evento disparado assim que a CR é aplicada:

```
2026-05-20T21:57:33Z  DEBUG  Created credentials secret myapp-db-credentials
2026-05-20T21:57:33Z  DEBUG  Created StatefulSet myapp-db with 1 replica(s)
```

CR em estado `Running` após ~60s:

```
$ kubectl get pgdb

NAME       PHASE     READY   VERSION   AGE
myapp-db   Running   1       16        60s
```

Recursos criados automaticamente:

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

PostgreSQL 16 respondendo dentro do pod:

```
$ kubectl exec myapp-db-0 -- psql -U myapp myapp -c "SELECT version();"

 PostgreSQL 16.14 on x86_64-pc-linux-gnu, compiled by gcc 14.2.0, 64-bit
```

---

## Spec da CR

```yaml
spec:
  version: "16"           # "14" | "15" | "16" | "17"
  replicas: 1             # 1 = standalone · 2+ = HA com streaming replication
  database: appdb         # nome do banco inicial
  username: appuser       # dono do banco

  storage:
    size: 10Gi
    storageClassName: fast-ssd   # opcional, usa o default do cluster

  resources:              # opcional
    requests:
      cpu: 100m
      memory: 256Mi
    limits:
      cpu: "1"
      memory: 512Mi

  passwordSecretRef:      # opcional — omitir gera senha automática
    name: meu-secret
    key: password
```

### Status

O operator mantém `.status` atualizado com o estado real do cluster:

```
Status:
  Phase:          Running          # Pending | Running | Failed
  Ready Replicas: 1
  Secret Name:    myapp-db-credentials
  Service Name:   myapp-db-svc
  Conditions:
    Type:     Ready
    Status:   True
    Reason:   AllReplicasReady
    Message:  1/1 replicas ready
```

---

## Usando as credenciais

O Secret gerado tem quatro chaves:

```
username   → nome do usuário
password   → senha aleatória (24 bytes, base64url)
database   → nome do banco
dsn        → postgres://user:pass@svc:5432/db?sslmode=disable
```

Injetar o DSN no seu app:

```yaml
env:
  - name: DATABASE_URL
    valueFrom:
      secretKeyRef:
        name: myapp-db-credentials
        key: dsn
```

---

## Comandos úteis

```bash
# sessão interativa
kubectl exec -it myapp-db-0 -- psql -U myapp myapp

# ver o DSN decodificado
kubectl get secret myapp-db-credentials -o jsonpath="{.data.dsn}" | base64 -d

# escalar para HA
kubectl patch pgdb myapp-db --type=merge -p '{"spec":{"replicas":3}}'

# port-forward para teste local
kubectl port-forward svc/myapp-db-svc 5432:5432

# logs do operator
kubectl -n postgres-operator-system logs -f deployment/postgres-operator
```

---

## Estrutura do projeto

```
├── api/v1alpha1/            tipos da CRD + DeepCopy gerado
├── cmd/main.go              entrypoint do manager
├── internal/controller/
│   ├── postgresdatabase_controller.go   reconciler + finalizer + status
│   └── resources.go                     builders de StatefulSet, Services
├── config/
│   ├── crd/bases/           schema OpenAPI v3
│   ├── rbac/                ClusterRole, Binding, ServiceAccount
│   ├── manager/             Deployment do operator
│   └── samples/             CRs de exemplo
└── Dockerfile               multi-stage · distroless runtime
```

## License

Apache 2.0
