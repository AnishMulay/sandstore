#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
KUBE_CONTEXT="${KUBE_CONTEXT:-docker-desktop}"
K8S_NAMESPACE="${K8S_NAMESPACE:-sandstore-test}"
SANDSTORE_STATEFULSET="${SANDSTORE_STATEFULSET:-sandstore}"
K8S_IMAGE="${K8S_IMAGE:-sandstore-node:cluster-local}"
JOB_NAME="${JOB_NAME:-sandstore-open-smoke}"
JOB_TIMEOUT_SECONDS="${JOB_TIMEOUT_SECONDS:-900}"
SANDSTORE_SEEDS="${SANDSTORE_SEEDS:-${SANDSTORE_STATEFULSET}-0.${SANDSTORE_STATEFULSET}-headless:8080,${SANDSTORE_STATEFULSET}-1.${SANDSTORE_STATEFULSET}-headless:8080,${SANDSTORE_STATEFULSET}-2.${SANDSTORE_STATEFULSET}-headless:8080}"

log() {
  printf '%s\n' "$*"
}

cleanup_job() {
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" delete job "${JOB_NAME}" --ignore-not-found=true >/dev/null 2>&1 || true
}

apply_job() {
  cleanup_job

  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" apply -f - >/dev/null <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: ${JOB_NAME}
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
      restartPolicy: Never
      containers:
        - name: ${JOB_NAME}
          image: ${K8S_IMAGE}
          imagePullPolicy: IfNotPresent
          command:
            - /usr/local/bin/smoke
          env:
            - name: SANDSTORE_SEEDS
              value: ${SANDSTORE_SEEDS}
EOF
}

wait_for_job() {
  local start_ts
  start_ts="$(date +%s)"

  while true; do
    local succeeded failed
    succeeded="$(kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" get job "${JOB_NAME}" -o jsonpath='{.status.succeeded}' 2>/dev/null || true)"
    failed="$(kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" get job "${JOB_NAME}" -o jsonpath='{.status.failed}' 2>/dev/null || true)"

    succeeded="${succeeded:-0}"
    failed="${failed:-0}"

    if [[ "${succeeded}" =~ ^[0-9]+$ ]] && (( succeeded > 0 )); then
      return 0
    fi
    if [[ "${failed}" =~ ^[0-9]+$ ]] && (( failed > 0 )); then
      return 1
    fi
    if (( $(date +%s) - start_ts >= JOB_TIMEOUT_SECONDS )); then
      return 124
    fi

    sleep 2
  done
}

print_failure() {
  log "Smoke job ${JOB_NAME} failed in namespace ${K8S_NAMESPACE}."
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" logs "job/${JOB_NAME}" --all-containers=true --timestamps=true || true
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" describe "job/${JOB_NAME}" || true
  kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" get pods -l "job-name=${JOB_NAME}" -o wide || true
}

main() {
  trap cleanup_job EXIT

  log "Running Kubernetes smoke job ${JOB_NAME} in namespace ${K8S_NAMESPACE}..."
  log "Repo root: ${REPO_ROOT}"
  apply_job

  if wait_for_job; then
    kubectl --context="${KUBE_CONTEXT}" -n "${K8S_NAMESPACE}" logs "job/${JOB_NAME}" --all-containers=true
    log "Smoke job ${JOB_NAME} completed successfully."
    return 0
  fi

  print_failure
  return 1
}

main "$@"
