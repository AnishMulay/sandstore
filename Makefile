KUBE_CONTEXT ?= docker-desktop

# Variables
PROFILE ?= default-etcd
ARCH ?= amd64
GOOS ?= linux
GOARCH ?= amd64
REGISTRY_URL ?=
K8S_IMAGE ?= $(REGISTRY_URL)/sandstore-node:$(PROFILE)-latest
K8S_LOCAL_IMAGE ?= sandstore-node:cluster-local
K8S_TEST_IMAGE ?= sandstore-test:cluster-local
K8S_MANIFEST_DIR ?= deploy/k8s
K8S_TEST_NAMESPACE_PREFIX ?= sandstore-test
DOCKER_COMPOSE_FILES := deploy/docker/docker-compose.yaml deploy/docker/docker-compose-durability.yaml deploy/docker/etcd/docker-compose.yaml
CLEAN_PORTS := 2379 2380 9001 9002 9003 8080 8081 8082

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
DURABILITY_SMOKE_BINARY=bin/durability-smoke
LEGACY_CLIENT_BINARY=client
LEGACY_MCP_BINARY=mcp
LEGACY_OPEN_SMOKE_BINARY=open_smoke
LEGACY_DURABILITY_SMOKE_BINARY=durability_smoke

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

.PHONY: durability-smoke
durability-smoke:
	@set -e; \
	compose_file=deploy/docker/docker-compose-durability.yaml; \
	docker compose -f $$compose_file up --build -d etcd etcd-init node-1 node-2 node-3; \
	set +e; \
	docker compose -f $$compose_file run --rm --no-deps smoke-test; \
	status=$$?; \
	set -e; \
	docker compose -f $$compose_file down -v --remove-orphans; \
	exit $$status

# Build Docker image
.PHONY: docker-build
docker-build:
	docker build --platform linux/$(ARCH) --build-arg TAGS="$(TAGS)" --build-arg GOOS=$(GOOS) --build-arg GOARCH=$(ARCH) -t sandstore-node:latest -f deploy/docker/Dockerfile .

.PHONY: docker-build-test
docker-build-test:
	docker build --platform linux/$(ARCH) --target test-runner --build-arg TAGS="$(TAGS)" --build-arg GOOS=$(GOOS) --build-arg GOARCH=$(ARCH) -t $(K8S_TEST_IMAGE) -f deploy/docker/Dockerfile .

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
	kubectl --context=$(KUBE_CONTEXT) set image statefulset/sandstore sandstore=$(K8S_IMAGE); \
	kubectl --context=$(KUBE_CONTEXT) apply -f $(K8S_MANIFEST_DIR)/service-nodeport.yaml

# Kubernetes Flow 4: The Iteration Loop (Stateful Rolling Update)
.PHONY: k8s-update
k8s-update:
	@set -eu; \
	kubectl --context=$(KUBE_CONTEXT) get statefulset sandstore >/dev/null; \
	$(MAKE) --no-print-directory k8s-build PROFILE="$(PROFILE)" REGISTRY_URL="$(REGISTRY_URL)" ARCH="$(ARCH)"; \
	kubectl --context=$(KUBE_CONTEXT) set image statefulset/sandstore sandstore=$(K8S_IMAGE); \
	kubectl --context=$(KUBE_CONTEXT) rollout restart statefulset/sandstore; \
	kubectl --context=$(KUBE_CONTEXT) rollout status statefulset/sandstore --timeout=120s || { \
		echo "Rollout failed or timed out. Inspect crash logs with: make k8s-logs-crash POD=<pod_name> KUBE_CONTEXT=$(KUBE_CONTEXT)"; \
		exit 1; \
	}

.PHONY: test-cluster
test-cluster:
	KUBE_CONTEXT="$(KUBE_CONTEXT)" \
	PROFILE="$(PROFILE)" \
	ARCH="$(ARCH)" \
	GOOS="$(GOOS)" \
	GOARCH="$(GOARCH)" \
	K8S_IMAGE="$(K8S_LOCAL_IMAGE)" \
	K8S_TEST_IMAGE="$(K8S_TEST_IMAGE)" \
	K8S_TEST_NAMESPACE_PREFIX="$(K8S_TEST_NAMESPACE_PREFIX)" \
	./scripts/dev/test-cluster.sh

.PHONY: cluster-up
cluster-up:
	KUBE_CONTEXT="$(KUBE_CONTEXT)" \
	PROFILE="$(PROFILE)" \
	ARCH="$(ARCH)" \
	GOOS="$(GOOS)" \
	GOARCH="$(GOARCH)" \
	K8S_IMAGE="$(K8S_LOCAL_IMAGE)" \
	K8S_TEST_IMAGE="$(K8S_TEST_IMAGE)" \
	K8S_TEST_NAMESPACE_PREFIX="$(K8S_TEST_NAMESPACE_PREFIX)" \
	K8S_NAMESPACE="$${K8S_NAMESPACE:-$(K8S_TEST_NAMESPACE_PREFIX)}" \
	./scripts/dev/cluster-up.sh

.PHONY: cluster-down
cluster-down:
	KUBE_CONTEXT="$(KUBE_CONTEXT)" \
	K8S_TEST_NAMESPACE_PREFIX="$(K8S_TEST_NAMESPACE_PREFIX)" \
	K8S_NAMESPACE="$${K8S_NAMESPACE:-$(K8S_TEST_NAMESPACE_PREFIX)}" \
	./scripts/dev/cluster-down.sh

.PHONY: smoke-test
smoke-test:
	@set -eu; \
	K8S_NAMESPACE="$${K8S_NAMESPACE:-$(K8S_TEST_NAMESPACE_PREFIX)}"; \
	K8S_IMAGE="$${K8S_IMAGE:-$(K8S_LOCAL_IMAGE)}"; \
	kubectl --context="$(KUBE_CONTEXT)" -n "$$K8S_NAMESPACE" delete job sandstore-open-smoke --ignore-not-found=true >/dev/null 2>&1 || true; \
	printf '%s\n' \
	  'apiVersion: batch/v1' \
	  'kind: Job' \
	  'metadata:' \
	  '  name: sandstore-open-smoke' \
	  '  labels:' \
	  '    app: sandstore' \
	  '    app.kubernetes.io/name: sandstore' \
	  'spec:' \
	  '  backoffLimit: 0' \
	  '  template:' \
	  '    metadata:' \
	  '      labels:' \
	  '        app: sandstore' \
	  '        app.kubernetes.io/name: sandstore' \
	  '    spec:' \
	  '      restartPolicy: Never' \
	  '      containers:' \
	  '        - name: sandstore-open-smoke' \
	  '          image: '"$$K8S_IMAGE" \
	  '          imagePullPolicy: IfNotPresent' \
	  '          command:' \
	  '            - /usr/local/bin/smoke' \
	  '          env:' \
	  '            - name: SANDSTORE_SEEDS' \
	  '              value: sandstore-0.sandstore-headless:8080,sandstore-1.sandstore-headless:8080,sandstore-2.sandstore-headless:8080' \
	  | kubectl --context="$(KUBE_CONTEXT)" -n "$$K8S_NAMESPACE" apply -f - >/dev/null; \
	kubectl --context="$(KUBE_CONTEXT)" -n "$$K8S_NAMESPACE" wait --for=condition=complete job/sandstore-open-smoke --timeout=900s; \
	kubectl --context="$(KUBE_CONTEXT)" -n "$$K8S_NAMESPACE" logs job/sandstore-open-smoke --all-containers=true

.PHONY: port-forward-prometheus
port-forward-prometheus:
	@set -eu; \
	K8S_NAMESPACE="$${K8S_NAMESPACE:-$(K8S_TEST_NAMESPACE_PREFIX)}"; \
	kubectl --context="$(KUBE_CONTEXT)" -n "$$K8S_NAMESPACE" port-forward service/prometheus 9090:9090

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

.PHONY: clean-runtime
clean-runtime:
	@set -eu; \
	echo "Stopping local sandstore processes..."; \
	pkill -f '[s]andstore' >/dev/null 2>&1 || true; \
	pkill -f '[o]pen-smoke' >/dev/null 2>&1 || true; \
	pkill -f '[d]urability_smoke' >/dev/null 2>&1 || true; \
	pkill -f '[d]urability-smoke' >/dev/null 2>&1 || true; \
	if docker info >/dev/null 2>&1; then \
		echo "Docker daemon reachable; tearing down project compose stacks..."; \
		for compose_file in $(DOCKER_COMPOSE_FILES); do \
			if [ -f "$$compose_file" ]; then \
				echo "  - docker compose -f $$compose_file down -v --remove-orphans"; \
				if ! docker compose -f "$$compose_file" down -v --remove-orphans; then \
					echo "WARNING: failed to tear down $$compose_file" >&2; \
				fi; \
			fi; \
		done; \
	else \
		echo "WARNING: Docker daemon is unavailable; skipping compose teardown." >&2; \
		echo "WARNING: If you previously ran Docker-based sandstore flows, containers or port mappings may remain." >&2; \
	fi; \
	if command -v lsof >/dev/null 2>&1; then \
		lingering_ports=""; \
		for port in $(CLEAN_PORTS); do \
			if lsof -nP -iTCP:$$port -sTCP:LISTEN >/dev/null 2>&1; then \
				lingering_ports="$$lingering_ports $$port"; \
			fi; \
		done; \
		if [ -n "$$lingering_ports" ]; then \
			echo "WARNING: project-related ports still listening:$$lingering_ports" >&2; \
			echo "Inspect with: lsof -nP -iTCP -sTCP:LISTEN | egrep ':(2379|2380|9001|9002|9003|8080|8081|8082) '" >&2; \
		else \
			echo "Project ports are clear."; \
		fi; \
	fi

.PHONY: clean-artifacts
clean-artifacts:
	@for cache_dir in ./.gomodcache ./.gomodcache-local; do \
		if [ -e "$$cache_dir" ]; then \
			chmod -R u+w "$$cache_dir" 2>/dev/null || true; \
		fi; \
	done
	rm -rf ./bin ./run ./logs ./chunks ./.gocache ./.gomodcache ./.gocache-local ./.gomodcache-local
	rm -f $(SANDSTORE_BINARY) $(CLIENT_BINARY) $(MCP_BINARY) $(OPEN_SMOKE_BINARY) $(DURABILITY_SMOKE_BINARY) $(LEGACY_CLIENT_BINARY) $(LEGACY_MCP_BINARY) $(LEGACY_OPEN_SMOKE_BINARY) $(LEGACY_DURABILITY_SMOKE_BINARY) config.yaml

# clean: full local reset (containers + generated artifacts + runtime data)
.PHONY: clean
clean: clean-runtime clean-artifacts
