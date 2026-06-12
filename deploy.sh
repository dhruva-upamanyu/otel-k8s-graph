#!/usr/bin/env bash
set -euo pipefail

# Build + push all three graph images, then install/upgrade the Helm release.
# Each image is pushed with two tags: the bumped version (from the VERSION
# file) and `latest`. The chart defaults to `latest`; this script pins the
# exact version at install time so every deploy rolls the pods.
#
#   REGISTRY=<registry> ./deploy.sh
#
# Optional: NAMESPACE (default "default"), RELEASE (default "graph"),
# VERSION=x.y.z to set an explicit version instead of bumping the patch.
# Prerequisites: docker (with buildx), helm, a kubectl context on the target
# cluster, and registry auth.

: "${REGISTRY:?REGISTRY is required, e.g. REGISTRY=ghcr.io/<you> ./deploy.sh}"

CHART="helm/graph"
RELEASE="${RELEASE:-graph}"
NAMESPACE="${NAMESPACE:-default}"
SERVICES=(graph-k8s graph-otel graph-read)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

CURRENT_VERSION="$(cat VERSION)"
if [[ -n "${VERSION:-}" ]]; then
  NEW_VERSION="$VERSION"
else
  IFS='.' read -r MAJOR MINOR PATCH <<< "$CURRENT_VERSION"
  NEW_VERSION="${MAJOR}.${MINOR}.$((PATCH + 1))"
fi
echo "Version: ${CURRENT_VERSION} -> ${NEW_VERSION}"

# Build + push each image (the compile stage is shared/cached across targets).
for SVC in "${SERVICES[@]}"; do
  echo ""
  echo "==> Building ${SVC}:${NEW_VERSION} (+ latest)"
  docker buildx build --platform linux/amd64 --target "${SVC}" \
    -t "${REGISTRY}/${SVC}:${NEW_VERSION}" \
    -t "${REGISTRY}/${SVC}:latest" \
    --push .
done

echo ""
echo "==> helm upgrade --install ${RELEASE}"
helm upgrade --install "${RELEASE}" "${CHART}" \
  --namespace "${NAMESPACE}" --create-namespace \
  --set image.registry="${REGISTRY}" \
  --set image.tag="${NEW_VERSION}"

# Persist the version only after a successful install, so a failed deploy
# retries the same version instead of skipping it.
echo "${NEW_VERSION}" > VERSION

echo ""
echo "Done. graph-k8s, graph-otel, graph-read deployed at ${NEW_VERSION}."
