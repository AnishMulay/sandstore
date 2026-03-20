#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-docker-desktop}"
K8S_TEST_NAMESPACE_PREFIX="${K8S_TEST_NAMESPACE_PREFIX:-sandstore-test}"
K8S_NAMESPACE="${K8S_NAMESPACE:-${K8S_TEST_NAMESPACE_PREFIX}}"

log() {
  printf '%s\n' "$*"
}

main() {
  log "Cleaning up namespace ${K8S_NAMESPACE}..."
  kubectl --context="${KUBE_CONTEXT}" delete namespace "${K8S_NAMESPACE}" --ignore-not-found=true --wait=false >/dev/null 2>&1 || true

  if kubectl --context="${KUBE_CONTEXT}" get namespace "${K8S_NAMESPACE}" >/dev/null 2>&1; then
    if ! kubectl --context="${KUBE_CONTEXT}" wait --for=delete "namespace/${K8S_NAMESPACE}" --timeout=240s >/dev/null 2>&1; then
      log "Namespace ${K8S_NAMESPACE} did not delete cleanly within timeout."
      exit 1
    fi
  fi

  start_ts="$(date +%s)"
  while true; do
    lingering="$(kubectl --context="${KUBE_CONTEXT}" get pv -o custom-columns=NAME:.metadata.name,CLAIM_NAMESPACE:.spec.claimRef.namespace --no-headers 2>/dev/null | awk -v ns="${K8S_NAMESPACE}" '$2 == ns { print $1 }')"
    if [[ -z "${lingering}" ]]; then
      break
    fi

    if (( $(date +%s) - start_ts >= 240 )); then
      log "Persistent volumes still reference namespace ${K8S_NAMESPACE}:"
      printf '%s\n' "${lingering}"
      exit 1
    fi

    sleep 2
  done
}

main "$@"
