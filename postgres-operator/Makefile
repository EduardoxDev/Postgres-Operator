IMG ?= ghcr.io/example/postgres-operator:latest

# Tool binaries
CONTROLLER_GEN ?= $(shell which controller-gen 2>/dev/null || echo ./bin/controller-gen)
KUSTOMIZE      ?= $(shell which kustomize 2>/dev/null || echo ./bin/kustomize)

.PHONY: all build test fmt vet generate manifests install uninstall deploy undeploy docker-build docker-push

all: build

## Build the manager binary
build: fmt vet
	go build -o bin/manager ./cmd/main.go

## Run tests
test: fmt vet
	go test ./... -v -count=1

## Format source code
fmt:
	go fmt ./...

## Run go vet
vet:
	go vet ./...

## Regenerate DeepCopy methods (requires controller-gen)
generate:
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

## Regenerate CRD manifests (requires controller-gen)
manifests:
	$(CONTROLLER_GEN) \
		rbac:roleName=postgres-operator-role \
		crd \
		webhook \
		paths="./..." \
		output:crd:artifacts:config=config/crd/bases \
		output:rbac:artifacts:config=config/rbac

## Install CRDs into the current cluster
install: manifests
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

## Uninstall CRDs from the current cluster
uninstall: manifests
	$(KUSTOMIZE) build config/crd | kubectl delete --ignore-not-found=true -f -

## Deploy the operator to the current cluster
deploy: manifests
	$(KUSTOMIZE) build config/default | kubectl apply -f -

## Undeploy the operator from the current cluster
undeploy:
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found=true -f -

## Build the Docker image
docker-build:
	docker build -t $(IMG) .

## Push the Docker image
docker-push:
	docker push $(IMG)

## Build and push in one step
docker-build-push: docker-build docker-push

## Run the operator locally (against the current kubeconfig)
run: fmt vet
	go run ./cmd/main.go

## Install controller-gen locally if missing
.PHONY: controller-gen
controller-gen:
	GOBIN=$(shell pwd)/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.0

## Install kustomize locally if missing
.PHONY: kustomize
kustomize:
	GOBIN=$(shell pwd)/bin go install sigs.k8s.io/kustomize/kustomize/v5@v5.4.3

## Apply sample CRs to the cluster
samples:
	kubectl apply -f config/samples/

## Show status of all PostgresDatabase objects
status:
	kubectl get postgresdatabases -A -o wide
