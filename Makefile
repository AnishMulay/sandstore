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
DURATION ?= 60
BLOCK_SIZE ?= 4096
TOPOLOGY ?= hyperconverged

define check-topology
	+@test -d scripts/topologies/$(TOPOLOGY) || \
	  (echo ""; \
	   echo "  Error: topology '$(TOPOLOGY)' not found."; \
	   echo "  Available topologies: $$(ls scripts/topologies/)"; \
	   echo "  Usage: make $@ TOPOLOGY=<topology>"; \
	   echo ""; \
	   exit 1)
endef

.PHONY: help
help:
	@echo "  cluster-up       Bring up the cluster for a topology"
	@echo "                   Usage: make cluster-up TOPOLOGY=hyperconverged"
	@echo ""
	@echo "  cluster-down     Tear down the cluster for a topology"
	@echo "                   Usage: make cluster-down TOPOLOGY=hyperconverged"
	@echo ""
	@echo "  smoke-test       Run smoke tests for a topology"
	@echo "                   Usage: make smoke-test TOPOLOGY=hyperconverged"
	@echo ""
	@echo "  smoke-local      Run local smoke test (requires local etcd on localhost:2379)"
	@echo "                   Usage: make smoke-local TOPOLOGY=hyperconverged"
	@echo ""
	@echo "  test-cluster     Run integration tests for a topology"
	@echo "                   Usage: make test-cluster TOPOLOGY=hyperconverged"
	@echo ""
	@echo "  cluster          Run a local cluster for a topology"
	@echo "                   Usage: make cluster TOPOLOGY=hyperconverged"

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
	$(check-topology)
	./scripts/topologies/$(TOPOLOGY)/run-local.sh

# Run the client
.PHONY: client
client:
	@mkdir -p $(dir $(CLIENT_BINARY))
	go build -o $(CLIENT_BINARY) ./clients/client
	./$(CLIENT_BINARY)

.PHONY: bench
bench:
	go build -o bin/bench ./clients/bench
	./bin/bench --seeds=$(SEEDS) --concurrency=$(CONCURRENCY) \
		--duration=$(DURATION) --block-size=$(BLOCK_SIZE)

# Run Go tests
.PHONY: test
test:
	go test -v ./...

.PHONY: durability-smoke
durability-smoke:
	@set -e; \
	compose_file=deploy/docker/docker-compose-durability.yaml; \
	docker compose -f $$compose_file up --build -d etcd etcd-init sandstore-hyperconverged-1 sandstore-hyperconverged-2 sandstore-hyperconverged-3; \
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
	kubectl --context=$(KUBE_CONTEXT) delete pvc -l app=sandstore-hyperconverged --ignore-not-found=true; \
	if kubectl --context=$(KUBE_CONTEXT) get pod -l app=sandstore-hyperconverged --no-headers 2>/dev/null | grep -q .; then \
		kubectl --context=$(KUBE_CONTEXT) wait --for=delete pod -l app=sandstore-hyperconverged --timeout=60s || { \
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
	kubectl --context=$(KUBE_CONTEXT) delete job sandstore-hyperconverged-bootstrap-config --ignore-not-found=true; \
	kubectl --context=$(KUBE_CONTEXT) apply -f $(K8S_MANIFEST_DIR)/job-bootstrap.yaml; \
	kubectl --context=$(KUBE_CONTEXT) wait --for=condition=complete job/sandstore-hyperconverged-bootstrap-config --timeout=60s; \
	kubectl --context=$(KUBE_CONTEXT) apply -f $(K8S_MANIFEST_DIR)/statefulset-sandstore.yaml; \
	kubectl --context=$(KUBE_CONTEXT) set image statefulset/sandstore-hyperconverged sandstore=$(K8S_IMAGE); \
	kubectl --context=$(KUBE_CONTEXT) apply -f $(K8S_MANIFEST_DIR)/service-nodeport.yaml

# Kubernetes Flow 4: The Iteration Loop (Stateful Rolling Update)
.PHONY: k8s-update
k8s-update:
	@set -eu; \
	kubectl --context=$(KUBE_CONTEXT) get statefulset sandstore-hyperconverged >/dev/null; \
	$(MAKE) --no-print-directory k8s-build PROFILE="$(PROFILE)" REGISTRY_URL="$(REGISTRY_URL)" ARCH="$(ARCH)"; \
	kubectl --context=$(KUBE_CONTEXT) set image statefulset/sandstore-hyperconverged sandstore=$(K8S_IMAGE); \
	kubectl --context=$(KUBE_CONTEXT) rollout restart statefulset/sandstore-hyperconverged; \
	kubectl --context=$(KUBE_CONTEXT) rollout status statefulset/sandstore-hyperconverged --timeout=120s || { \
		echo "Rollout failed or timed out. Inspect crash logs with: make k8s-logs-crash POD=<pod_name> KUBE_CONTEXT=$(KUBE_CONTEXT)"; \
		exit 1; \
	}

.PHONY: test-cluster
test-cluster:
	$(check-topology)
	KUBE_CONTEXT="$(KUBE_CONTEXT)" \
	PROFILE="$(PROFILE)" \
	ARCH="$(ARCH)" \
	GOOS="$(GOOS)" \
	GOARCH="$(GOARCH)" \
	K8S_IMAGE="$(K8S_LOCAL_IMAGE)" \
	K8S_TEST_IMAGE="$(K8S_TEST_IMAGE)" \
	K8S_TEST_NAMESPACE_PREFIX="$(K8S_TEST_NAMESPACE_PREFIX)" \
	./scripts/topologies/$(TOPOLOGY)/test.sh

.PHONY: cluster-up
cluster-up:
	$(check-topology)
	KUBE_CONTEXT="$(KUBE_CONTEXT)" \
	PROFILE="$(PROFILE)" \
	ARCH="$(ARCH)" \
	GOOS="$(GOOS)" \
	GOARCH="$(GOARCH)" \
	K8S_IMAGE="$(K8S_LOCAL_IMAGE)" \
	K8S_TEST_IMAGE="$(K8S_TEST_IMAGE)" \
	K8S_TEST_NAMESPACE_PREFIX="$(K8S_TEST_NAMESPACE_PREFIX)" \
	K8S_NAMESPACE="$${K8S_NAMESPACE:-$(K8S_TEST_NAMESPACE_PREFIX)}" \
	./scripts/topologies/$(TOPOLOGY)/cluster-up.sh

.PHONY: cluster-down
cluster-down:
	$(check-topology)
	KUBE_CONTEXT="$(KUBE_CONTEXT)" \
	K8S_TEST_NAMESPACE_PREFIX="$(K8S_TEST_NAMESPACE_PREFIX)" \
	K8S_NAMESPACE="$${K8S_NAMESPACE:-$(K8S_TEST_NAMESPACE_PREFIX)}" \
	./scripts/topologies/$(TOPOLOGY)/cluster-down.sh

.PHONY: smoke-test
smoke-test:
	$(check-topology)
	./scripts/topologies/$(TOPOLOGY)/smoke.sh

.PHONY: smoke-local
smoke-local:
	$(check-topology)
	./scripts/topologies/$(TOPOLOGY)/smoke-local.sh

.PHONY: port-forward-prometheus
port-forward-prometheus:
	@set -eu; \
	K8S_NAMESPACE="$${K8S_NAMESPACE:-$(K8S_TEST_NAMESPACE_PREFIX)}"; \
	kubectl --context="$(KUBE_CONTEXT)" -n "$$K8S_NAMESPACE" port-forward service/prometheus 9090:9090

# External access: expose all 3 sandstore pods on Ubuntu LAN so MacBook bench can reach them.
# Usage:
#   make port-forward-nodes                         # auto-detect LAN IP from k8s node
#   make port-forward-nodes EXTERNAL_IP=10.0.0.5   # override with explicit IP
#   make port-forward-nodes EXTERNAL_IP=127.0.0.1  # same-machine testing on local laptop
#   Then from MacBook: make bench SEEDS=<printed-ip>:9080,<printed-ip>:9081,<printed-ip>:9082 CONCURRENCY=1
.PHONY: port-forward-nodes
port-forward-nodes:
	@set -eu; \
	K8S_NAMESPACE="$${K8S_NAMESPACE:-$(K8S_TEST_NAMESPACE_PREFIX)}"; \
	\
	if [ -n "$${EXTERNAL_IP:-}" ]; then \
		DETECTED_IP="$$EXTERNAL_IP"; \
		echo "Using provided EXTERNAL_IP=$$DETECTED_IP"; \
	else \
		DETECTED_IP=$$(kubectl --context="$(KUBE_CONTEXT)" get nodes \
			-o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null || true); \
		if [ -z "$$DETECTED_IP" ]; then \
			echo "ERROR: Could not auto-detect node IP. Re-run with: make port-forward-nodes EXTERNAL_IP=<ubuntu-lan-ip>"; \
			exit 1; \
		fi; \
		echo "Auto-detected node IP: $$DETECTED_IP"; \
	fi; \
	\
	echo "Patching sandstore-hyperconverged-config ConfigMap with ADVERTISE_HOST=$$DETECTED_IP and EXTERNAL_BASE_PORT=9080..."; \
	kubectl --context="$(KUBE_CONTEXT)" -n "$$K8S_NAMESPACE" patch configmap sandstore-hyperconverged-config \
		--type merge -p "{\"data\":{\"ADVERTISE_HOST\":\"$$DETECTED_IP\",\"EXTERNAL_BASE_PORT\":\"9080\"}}"; \
	\
	echo "Triggering rolling restart of sandstore-hyperconverged StatefulSet..."; \
	kubectl --context="$(KUBE_CONTEXT)" -n "$$K8S_NAMESPACE" rollout restart statefulset/sandstore-hyperconverged; \
	kubectl --context="$(KUBE_CONTEXT)" -n "$$K8S_NAMESPACE" rollout status statefulset/sandstore-hyperconverged --timeout=120s; \
	\
	echo "Starting port-forwards (0.0.0.0 bound so MacBook can reach Ubuntu)..."; \
	kubectl --context="$(KUBE_CONTEXT)" -n "$$K8S_NAMESPACE" \
		port-forward --address 0.0.0.0 pod/sandstore-hyperconverged-0 9080:8080 &\
	PF0=$$!; \
	kubectl --context="$(KUBE_CONTEXT)" -n "$$K8S_NAMESPACE" \
		port-forward --address 0.0.0.0 pod/sandstore-hyperconverged-1 9081:8080 &\
	PF1=$$!; \
	kubectl --context="$(KUBE_CONTEXT)" -n "$$K8S_NAMESPACE" \
		port-forward --address 0.0.0.0 pod/sandstore-hyperconverged-2 9082:8080 &\
	PF2=$$!; \
	\
	trap 'echo "Cleaning up port-forwards..."; kill $$PF0 $$PF1 $$PF2 2>/dev/null || true' INT TERM EXIT; \
	\
	echo ""; \
	echo "========================================"; \
	echo "Port-forwards active. From MacBook run:"; \
	echo "  make bench SEEDS=$$DETECTED_IP:9080,$$DETECTED_IP:9081,$$DETECTED_IP:9082 CONCURRENCY=1"; \
	echo "========================================"; \
	echo "Press Ctrl-C to stop port-forwards and revert."; \
	\
	wait $$PF0 $$PF1 $$PF2

# Kubernetes Flow 5: Observability & Post-Mortem Debugging
.PHONY: k8s-logs
k8s-logs:
	kubectl --context=$(KUBE_CONTEXT) logs -l app=sandstore-hyperconverged -f --max-log-requests=10

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
	rm -f ./bin/bench
	rm -f ./bench
	rm -rf ./run ./results
