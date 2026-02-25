KUBE_CONTEXT ?= minikube-laptop

# Variables
PROFILE ?= default-etcd
ARCH ?= amd64
GOOS ?= linux
GOARCH ?= amd64
REGISTRY_URL ?=
K8S_IMAGE ?= $(REGISTRY_URL)/sandstore-node:$(PROFILE)-latest
K8S_MANIFEST_DIR ?= deploy/k8s

SUPPORTED_PROFILES := default-etcd

ifeq ($(PROFILE),default-etcd)
TAGS := grpc etcd
else
$(error Unsupported PROFILE '$(PROFILE)'. Supported profiles: $(SUPPORTED_PROFILES))
endif

SANDSTORE_BINARY=sandstore
CLIENT_BINARY=bin/client
MCP_BINARY=sandstore-mcp
OPEN_SMOKE_BINARY=bin/open-smoke
LEGACY_CLIENT_BINARY=client
LEGACY_MCP_BINARY=mcp
LEGACY_OPEN_SMOKE_BINARY=open_smoke

# Generate protobuf files
.PHONY: proto
proto:
	PATH=$(PATH):$(shell go env GOPATH)/bin protoc --go_out=gen --go_opt=paths=source_relative \
		--go-grpc_out=gen --go-grpc_opt=paths=source_relative \
		proto/communication/communication.proto

# Build sandstore node
.PHONY: build
build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -tags="$(TAGS)" -o $(SANDSTORE_BINARY) ./cmd/sandstore

# Build MCP server
.PHONY: mcp
mcp:
	go build -o $(MCP_BINARY) ./clients/mcp

# Run simple server via cmd/sandstore
.PHONY: simple
simple:
	./scripts/dev/run-simple.sh

# Start 5-node Raft cluster
.PHONY: cluster
cluster:
	-./scripts/dev/run-5.sh

# Run the client
.PHONY: client
client:
	@mkdir -p $(dir $(CLIENT_BINARY))
	go build -o $(CLIENT_BINARY) ./clients/client
	./$(CLIENT_BINARY)

# Run Go tests
.PHONY: test
test:
	go test -v ./...

# Build Docker image
.PHONY: docker-build
docker-build:
	docker build --platform linux/$(ARCH) --build-arg TAGS="$(TAGS)" --build-arg GOOS=$(GOOS) --build-arg GOARCH=$(ARCH) -t sandstore-node:latest -f deploy/docker/Dockerfile .

# Kubernetes Flow 1: Image Build & Registry Push
.PHONY: k8s-build
k8s-build:
	@set -eu; \
	if [ -z "$(REGISTRY_URL)" ]; then \
		echo "REGISTRY_URL is required (example: docker.io/<namespace>)"; \
		exit 1; \
	fi; \
	docker info >/dev/null 2>&1 || { \
		echo "Docker daemon is not running"; \
		exit 1; \
	}; \
	if [ ! -f "$$HOME/.docker/config.json" ] || ! grep -q '"auths"' "$$HOME/.docker/config.json"; then \
		echo "Docker registry authentication not detected. Run 'docker login' first."; \
		exit 1; \
	fi; \
	docker build --platform linux/$(ARCH) \
		--build-arg TAGS="$(TAGS)" \
		--build-arg GO_FLAGS="-tags $(PROFILE)" \
		--build-arg GOOS=linux \
		--build-arg GOARCH=$(ARCH) \
		-t "$(K8S_IMAGE)" \
		-f deploy/docker/Dockerfile .; \
	docker push "$(K8S_IMAGE)"

# Kubernetes Flow 2: Absolute Teardown (Clean Slate Guarantee)
.PHONY: k8s-destroy
k8s-destroy:
	@set -eu; \
	if [ "$(KUBE_CONTEXT)" = "docker-desktop" ] || [ "$(KUBE_CONTEXT)" = "minikube" ]; then \
		echo "Warning: running k8s-destroy against local context '$(KUBE_CONTEXT)'"; \
	fi; \
	kubectl --context=$(KUBE_CONTEXT) delete -f $(K8S_MANIFEST_DIR)/ --ignore-not-found=true; \
	kubectl --context=$(KUBE_CONTEXT) delete pvc -l app=sandstore --ignore-not-found=true; \
	if kubectl --context=$(KUBE_CONTEXT) get pod -l app=sandstore --no-headers 2>/dev/null | grep -q .; then \
		kubectl --context=$(KUBE_CONTEXT) wait --for=delete pod -l app=sandstore --timeout=60s || { \
			echo "Teardown wait timed out. Inspect terminating resources and force-delete stuck pods/namespaces if needed."; \
			exit 1; \
		}; \
	fi

# Kubernetes Flow 3: Cluster Bootstrapping & Hardware Targeting
.PHONY: k8s-deploy
k8s-deploy: k8s-destroy
	@set -eu; \
	kubectl --context=$(KUBE_CONTEXT) apply -f $(K8S_MANIFEST_DIR)/configmap.yaml -f $(K8S_MANIFEST_DIR)/storageclass.yaml -f $(K8S_MANIFEST_DIR)/service-headless.yaml -f $(K8S_MANIFEST_DIR)/service-etcd.yaml; \
	kubectl --context=$(KUBE_CONTEXT) apply -f $(K8S_MANIFEST_DIR)/statefulset-etcd.yaml; \
	kubectl --context=$(KUBE_CONTEXT) rollout status statefulset/etcd-cluster --timeout=60s; \
	kubectl --context=$(KUBE_CONTEXT) delete job sandstore-bootstrap-config --ignore-not-found=true; \
	kubectl --context=$(KUBE_CONTEXT) apply -f $(K8S_MANIFEST_DIR)/job-bootstrap.yaml; \
	kubectl --context=$(KUBE_CONTEXT) wait --for=condition=complete job/sandstore-bootstrap-config --timeout=60s; \
	kubectl --context=$(KUBE_CONTEXT) apply -f $(K8S_MANIFEST_DIR)/statefulset-sandstore.yaml; \
	kubectl --context=$(KUBE_CONTEXT) apply -f $(K8S_MANIFEST_DIR)/service-nodeport.yaml

# Kubernetes Flow 4: The Iteration Loop (Stateful Rolling Update)
.PHONY: k8s-update
k8s-update:
	@set -eu; \
	kubectl --context=$(KUBE_CONTEXT) get statefulset sandstore >/dev/null; \
	$(MAKE) --no-print-directory k8s-build PROFILE="$(PROFILE)" REGISTRY_URL="$(REGISTRY_URL)" ARCH="$(ARCH)"; \
	kubectl --context=$(KUBE_CONTEXT) rollout restart statefulset/sandstore; \
	kubectl --context=$(KUBE_CONTEXT) rollout status statefulset/sandstore --timeout=120s || { \
		echo "Rollout failed or timed out. Inspect crash logs with: make k8s-logs-crash POD=<pod_name> KUBE_CONTEXT=$(KUBE_CONTEXT)"; \
		exit 1; \
	}

# Kubernetes Flow 5: Observability & Post-Mortem Debugging
.PHONY: k8s-logs
k8s-logs:
	kubectl --context=$(KUBE_CONTEXT) logs -l app=sandstore -f --max-log-requests=10

.PHONY: k8s-logs-crash
k8s-logs-crash:
	@set -eu; \
	if [ -z "$(POD)" ]; then \
		echo "POD=<pod_name> is required"; \
		exit 1; \
	fi; \
	kubectl --context=$(KUBE_CONTEXT) logs "$(POD)" --previous

# Kill running sandstore processes
.PHONY: kill
kill:
	-@pgrep -fal sandstore || true
	-@pkill -f sandstore || true

# clean: full local reset (containers + generated artifacts + runtime data)
.PHONY: clean
clean:
	-@docker compose -f deploy/docker/docker-compose.yaml down -v --remove-orphans >/dev/null 2>&1 || true
	-@docker compose -f deploy/docker/etcd/docker-compose.yaml down -v --remove-orphans >/dev/null 2>&1 || true
	rm -rf ./bin ./run ./logs ./chunks ./.gocache ./.gomodcache ./.gocache-local ./.gomodcache-local
	rm -f $(SANDSTORE_BINARY) $(CLIENT_BINARY) $(MCP_BINARY) $(OPEN_SMOKE_BINARY) $(LEGACY_CLIENT_BINARY) $(LEGACY_MCP_BINARY) $(LEGACY_OPEN_SMOKE_BINARY) config.yaml
