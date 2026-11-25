#!/bin/sh
# Usage: image.sh <instance> <documentdb version>
#
# Checks that the CloudNativePG Cluster of an Instance, and every one of its
# pods, runs <version> of DocumentDB in the image mode the operator chose when
# it created the Cluster: the combined PostgreSQL + DocumentDB image, or the
# extension image mounted as an ImageVolume. When E2E_IMAGE_MODE is set, the
# mode must be that one. Waits a few minutes for a rollout to finish.
set -eu

instance=$1
version=$2

cluster() {
  kubectl -n "$NAMESPACE" get clusters.postgresql.cnpg.io "$instance" -o jsonpath="$1"
}

mode=$(cluster '{.metadata.annotations.documentdb\.io/image-mode}')
case "$mode" in
  combined)
    want="/documentdb-combined:18-$version"
    cluster_image='{.spec.imageName}'
    pod_image='{.spec.containers[?(@.name=="postgres")].image}'
    ;;
  imageVolume)
    want="/documentdb:$version"
    cluster_image='{.spec.postgresql.extensions[0].image.reference}'
    pod_image='{.spec.volumes[*].image.reference}'
    ;;
  *)
    echo "unknown image mode \"$mode\"" >&2
    exit 1
    ;;
esac
echo "image mode: $mode"
if [ -n "${E2E_IMAGE_MODE:-}" ] && [ "$mode" != "$E2E_IMAGE_MODE" ]; then
  echo "expected image mode $E2E_IMAGE_MODE" >&2
  exit 1
fi

for attempt in $(seq 1 180); do
  stale=""
  image=$(cluster "$cluster_image")
  case "$image" in *"$want") ;; *) stale="Cluster:$image" ;; esac
  for pod in $(kubectl -n "$NAMESPACE" get pods -l cnpg.io/cluster="$instance",cnpg.io/podRole=instance -o name); do
    image=$(kubectl -n "$NAMESPACE" get "$pod" -o jsonpath="$pod_image")
    case "$image" in *"$want"*) ;; *) stale="$stale $pod:$image" ;; esac
  done
  if [ -z "$stale" ]; then
    echo "every pod runs $want"
    exit 0
  fi
  sleep 5
done
echo "expected images ending in $want, got: $stale" >&2
exit 1
