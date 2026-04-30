#!/usr/bin/env bash
# Build CoverPort-instrumented KubeVirt images.
#
# Builds all 4 core components with Go coverage instrumentation and
# packages them as container images. The images can be pushed to any
# registry and deployed to a test cluster for E2E coverage collection.
#
# Usage:
#   ./hack/coverport/build-instrumented.sh                     # build all, local only
#   ./hack/coverport/build-instrumented.sh --push              # build + push to registry
#   ./hack/coverport/build-instrumented.sh --registry quay.io/myorg  # custom registry
#   ./hack/coverport/build-instrumented.sh --components virt-api,virt-controller
#   CONTAINER_ENGINE=docker ./hack/coverport/build-instrumented.sh  # use docker
#
# Environment:
#   CONTAINER_ENGINE  podman or docker (default: podman)
#   REGISTRY          image registry prefix (default: localhost)
#   TAG               image tag (default: coverport-latest)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Defaults
ENGINE="${CONTAINER_ENGINE:-podman}"
REGISTRY="${REGISTRY:-localhost}"
TAG="${TAG:-coverport-latest}"
PUSH=false
ALL_COMPONENTS="virt-api virt-controller virt-handler virt-operator"
COMPONENTS="$ALL_COMPONENTS"

# Parse args
while [[ $# -gt 0 ]]; do
    case "$1" in
        --push)       PUSH=true; shift ;;
        --registry)   REGISTRY="$2"; shift 2 ;;
        --tag)        TAG="$2"; shift 2 ;;
        --components) COMPONENTS="${2//,/ }"; shift 2 ;;
        --help|-h)
            echo "Usage: $0 [--push] [--registry REGISTRY] [--tag TAG] [--components c1,c2]"
            exit 0 ;;
        *) echo "Unknown arg: $1"; exit 1 ;;
    esac
done

DOCKERFILE="$SCRIPT_DIR/Dockerfile.coverport"

echo "========================================"
echo " KubeVirt CoverPort Instrumented Build"
echo "========================================"
echo "Engine:     $ENGINE"
echo "Registry:   $REGISTRY"
echo "Tag:        $TAG"
echo "Components: $COMPONENTS"
echo "Dockerfile: $DOCKERFILE"
echo ""

BUILT=0
FAILED=0

for COMP in $COMPONENTS; do
    IMAGE="${REGISTRY}/kubevirt-coverport/${COMP}:${TAG}"
    echo "--- Building $COMP -> $IMAGE ---"

    if $ENGINE build \
        -f "$DOCKERFILE" \
        --build-arg "COMPONENT=$COMP" \
        -t "$IMAGE" \
        "$REPO_ROOT" 2>&1; then

        echo "  OK: $IMAGE"
        BUILT=$((BUILT + 1))

        if $PUSH; then
            echo "  Pushing $IMAGE ..."
            if $ENGINE push "$IMAGE" 2>&1; then
                echo "  Pushed: $IMAGE"
            else
                echo "  WARN: Push failed for $IMAGE (continuing)"
            fi
        fi
    else
        echo "  FAIL: $COMP build failed"
        FAILED=$((FAILED + 1))
    fi
    echo ""
done

echo "========================================"
echo " Summary"
echo "========================================"
echo "Built:  $BUILT"
echo "Failed: $FAILED"
echo ""

if [ "$PUSH" = false ]; then
    echo "Images are local only. To push, re-run with --push --registry <your-registry>"
fi

echo ""
echo "To deploy to a cluster:"
echo "  1. Push images to a registry accessible from your cluster"
echo "  2. Patch the KubeVirt deployment to use the instrumented images:"
echo ""
echo "     oc set image deployment/virt-api virt-api=${REGISTRY}/kubevirt-coverport/virt-api:${TAG} -n kubevirt"
echo "     oc set image deployment/virt-controller virt-controller=${REGISTRY}/kubevirt-coverport/virt-controller:${TAG} -n kubevirt"
echo "     oc set image daemonset/virt-handler virt-handler=${REGISTRY}/kubevirt-coverport/virt-handler:${TAG} -n kubevirt"
echo "     oc set image deployment/virt-operator virt-operator=${REGISTRY}/kubevirt-coverport/virt-operator:${TAG} -n kubevirt"
echo ""
echo "  3. Collect coverage from the dashboard:"
echo "     curl -X POST https://<dashboard>/api/coverage/collect-product?project=cnv"
