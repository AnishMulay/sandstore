#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
KUBE_CONTEXT="${KUBE_CONTEXT:-docker-desktop}"
PROFILE="${PROFILE:-default-etcd}"
ARCH="${ARCH:-amd64}"
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-amd64}"
K8S_IMAGE="${K8S_IMAGE:-sandstore-node:cluster-local}"
K8S_TEST_IMAGE="${K8S_TEST_IMAGE:-sandstore-test:cluster-local}"
K8S_TEST_NAMESPACE_PREFIX="${K8S_TEST_NAMESPACE_PREFIX:-sandstore-test}"
K8S_NAMESPACE="${K8S_NAMESPACE:-${K8S_TEST_NAMESPACE_PREFIX}-$(date +%s)}"
K8S_MANIFEST_DIR="${K8S_MANIFEST_DIR:-deploy/k8s}"

TEST_STATUS=0
FAILURES=()
OPEN_JOB_NAME="sandstore-open-smoke"
DURABILITY_JOB_NAME="sandstore-durability-smoke"
LEADER_JOB_NAME="sandstore-leader-ready"

log() {
  printf '%s\n' "$*"
}

cleanup() {
  set +e

  log
  log "Cleaning up namespace ${K8S_NAMESPACE}..."
  kubectl --context="${KUBE_CONTEXT}" delete namespace "${K8S_NAMESPACE}" --ignore-not-found=true --wait=false >/dev/null 2>&1 || true

  if kubectl --context="${KUBE_CONTEXT}" get namespace "${K8S_NAMESPACE}" >/dev/null 2>&1; then
    if ! kubectl --context="${KUBE_CONTEXT}" wait --for=delete "namespace/${K8S_NAMESPACE}" --timeout=240s >/dev/null 2>&1; then
      log "Namespace ${K8S_NAMESPACE} did not delete cleanly within timeout."
      TEST_STATUS=1
    fi
  fi

  local start_ts
  start_ts="$(date +%s)"
  while true; do
    local lingering
    lingering="$(kubectl --context="${KUBE_CONTEXT}" get pv -o custom-columns=NAME:.metadata.name,CLAIM_NAMESPACE:.spec.claimRef.namespace --no-headers 2>/dev/null | awk -v ns="${K8S_NAMESPACE}" '$2 == ns { print $1 }')"
    if [[ -z "${lingering}" ]]; then
      break
    fi

    if (( $(date +%s) - start_ts >= 240 )); then
      log "Persistent volumes still reference namespace ${K8S_NAMESPACE}:"
      printf '%s\n' "${lingering}"
      TEST_STATUS=1
      break
    fi

    sleep 2
  done

  if (( ${#FAILURES[@]} > 0 )); then
    log
    log "Failures:"
    printf ' - %s\n' "${FAILURES[@]}"
  fi

  exit "${TEST_STATUS}"
}
trap cleanup EXIT INT TERM

require_tooling() {
  command -v docker >/dev/null 2>&1 || { log "docker is required"; exit 1; }
  command -v kubectl >/dev/null 2>&1 || { log "kubectl is required"; exit 1; }
}

build_images() {
  log "Building local Kubernetes images..."
  docker build \
    --platform "linux/${ARCH}" \
    --target runner \
    --build-arg TAGS="grpc etcd" \
    --build-arg GOOS="${GOOS}" \
    --build-arg GOARCH="${GOARCH}" \
    -t "${K8S_IMAGE}" \
    -f "${ROOT}/deploy/docker/Dockerfile" \
    "${ROOT}"

  docker build \
    --platform "linux/${ARCH}" \
    --target test-runner \
    --build-arg TAGS="grpc etcd" \
    --build-arg GOOS="${GOOS}" \
    --build-arg GOARCH="${GOARCH}" \
    -t "${K8S_TEST_IMAGE}" \
    -f "${ROOT}/deploy/docker/Dockerfile" \
    "${ROOT}"
}

create_namespace() {
  log "Creating namespace ${K8S_NAMESPACE}..."
  kubectl --context="${KUBE_CONTEXT}" create namespace "${K8S_NAMESPACE}" >/dev/null
}

apply_manifests() {
  log "Applying storage class..."
  kubectl --context="${KUBE_CONTEXT}" apply -f "${ROOT}/${K8S_MANIFEST_DIR}/storageclass.yaml" >/dev/null

  log "Deploying etcd cluster..."
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" apply \
    -f "${ROOT}/${K8S_MANIFEST_DIR}/service-etcd.yaml" \
    -f "${ROOT}/${K8S_MANIFEST_DIR}/statefulset-etcd.yaml" >/dev/null
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" rollout status statefulset/etcd-cluster --timeout=240s

  log "Applying sandstore config, service, and test RBAC..."
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" apply \
    -f "${ROOT}/${K8S_MANIFEST_DIR}/configmap.yaml" \
    -f "${ROOT}/${K8S_MANIFEST_DIR}/service-headless.yaml" \
    -f "${ROOT}/${K8S_MANIFEST_DIR}/rbac-cluster-tests.yaml" >/dev/null

  log "Bootstrapping cluster membership in etcd..."
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" delete job sandstore-bootstrap-config --ignore-not-found=true >/dev/null
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" apply -f "${ROOT}/${K8S_MANIFEST_DIR}/job-bootstrap.yaml" >/dev/null
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" wait --for=condition=complete job/sandstore-bootstrap-config --timeout=180s

  log "Deploying sandstore raft cluster..."
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" apply -f "${ROOT}/${K8S_MANIFEST_DIR}/statefulset-sandstore.yaml" >/dev/null
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" set image statefulset/sandstore "sandstore=${K8S_IMAGE}" >/dev/null
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" rollout status statefulset/sandstore --timeout=240s
}

apply_test_job() {
  local job_name="$1"
  local test_name="$2"

  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" delete job "${job_name}" --ignore-not-found=true >/dev/null 2>&1 || true
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" apply -f - >/dev/null <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: ${job_name}
  labels:
    app: sandstore
    app.kubernetes.io/name: sandstore
spec:
  backoffLimit: 0
  template:
    metadata:
      labels:
        app: sandstore
        app.kubernetes.io/name: sandstore
    spec:
      serviceAccountName: sandstore-test-runner
      restartPolicy: Never
      containers:
        - name: ${job_name}
          image: ${K8S_TEST_IMAGE}
          imagePullPolicy: IfNotPresent
          args:
            - -test.v
            - -test.timeout=20m
            - -test.run
            - ^${test_name}\$
          env:
            - name: POD_NAMESPACE
              valueFrom:
                fieldRef:
                  fieldPath: metadata.namespace
            - name: SANDSTORE_SEEDS
              value: sandstore-0.sandstore-headless:8080,sandstore-1.sandstore-headless:8080,sandstore-2.sandstore-headless:8080
            - name: SANDSTORE_NODE_ADDRS
              value: sandstore-0.sandstore-headless:8080,sandstore-1.sandstore-headless:8080,sandstore-2.sandstore-headless:8080
            - name: SANDSTORE_STATEFULSET
              value: sandstore
            - name: DURABILITY_SNAPSHOT_FILE_COUNT
              value: "260"
EOF
}

wait_for_job() {
  local job_name="$1"
  local timeout_secs="$2"
  local start_ts
  start_ts="$(date +%s)"

  while true; do
    local succeeded failed
    succeeded="$(kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" get job "${job_name}" -o jsonpath='{.status.succeeded}' 2>/dev/null || true)"
    failed="$(kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" get job "${job_name}" -o jsonpath='{.status.failed}' 2>/dev/null || true)"

    succeeded="${succeeded:-0}"
    failed="${failed:-0}"

    if [[ "${succeeded}" =~ ^[0-9]+$ ]] && (( succeeded > 0 )); then
      return 0
    fi
    if [[ "${failed}" =~ ^[0-9]+$ ]] && (( failed > 0 )); then
      return 1
    fi
    if (( $(date +%s) - start_ts >= timeout_secs )); then
      return 124
    fi

    sleep 2
  done
}

print_job_output() {
  local job_name="$1"

  log
  log "===== ${job_name} logs ====="
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" logs "job/${job_name}" --all-containers=true --timestamps=true || true
  log "===== ${job_name} describe ====="
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" describe "job/${job_name}" || true
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" get pods -l "job-name=${job_name}" -o wide || true
}

run_test_job() {
  local job_name="$1"
  local test_name="$2"
  local timeout_secs="$3"

  log "Running ${test_name} in Kubernetes job ${job_name}..."
  apply_test_job "${job_name}" "${test_name}"

  if wait_for_job "${job_name}" "${timeout_secs}"; then
    kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" logs "job/${job_name}" --all-containers=true
    return 0
  fi

  TEST_STATUS=1
  FAILURES+=("${test_name} failed in job ${job_name}")
  print_job_output "${job_name}"
  return 1
}

main() {
  require_tooling
  build_images
  create_namespace
  apply_manifests

  if ! run_test_job "${LEADER_JOB_NAME}" "TestLeaderElectionReady" 300; then
    return 1
  fi

  if ! run_test_job "${OPEN_JOB_NAME}" "TestOpenSmoke" 900; then
    return 1
  fi

  run_test_job "${DURABILITY_JOB_NAME}" "TestDurabilitySmoke" 1200
}

main "$@"
