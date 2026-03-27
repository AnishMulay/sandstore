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
K8S_NAMESPACE="${K8S_NAMESPACE:-${K8S_TEST_NAMESPACE_PREFIX}}"
K8S_MANIFEST_DIR="${K8S_MANIFEST_DIR:-deploy/k8s}"

log() {
  printf '%s\n' "$*"
}

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
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" delete job sandstore-hyperconverged-bootstrap-config --ignore-not-found=true >/dev/null
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" apply -f "${ROOT}/${K8S_MANIFEST_DIR}/job-bootstrap.yaml" >/dev/null
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" wait --for=condition=complete job/sandstore-hyperconverged-bootstrap-config --timeout=180s

  log "Deploying sandstore raft cluster..."
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" apply -f "${ROOT}/${K8S_MANIFEST_DIR}/statefulset-sandstore.yaml" >/dev/null
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" set image statefulset/sandstore-hyperconverged "sandstore=${K8S_IMAGE}" >/dev/null
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" rollout status statefulset/sandstore-hyperconverged --timeout=240s

  log "Deploying Prometheus..."
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" apply \
    -f "${ROOT}/${K8S_MANIFEST_DIR}/prometheus-rbac.yaml" >/dev/null
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" apply \
    -f "${ROOT}/${K8S_MANIFEST_DIR}/prometheus-configmap.yaml" \
    -f "${ROOT}/${K8S_MANIFEST_DIR}/deployment-prometheus.yaml" \
    -f "${ROOT}/${K8S_MANIFEST_DIR}/service-prometheus.yaml" >/dev/null
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" rollout status deployment/prometheus --timeout=240s
}

main() {
  require_tooling
  build_images
  create_namespace
  apply_manifests
}

main "$@"
