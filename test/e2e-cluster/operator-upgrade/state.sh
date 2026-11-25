#!/bin/sh
# Usage: state.sh <instance>
#
# Prints what an operator upgrade must leave alone: the PostgreSQL image of the
# Instance's CloudNativePG Cluster, and the UID and restart counts of each of
# its pods.
set -eu

instance=$1
kubectl -n "$NAMESPACE" get clusters.postgresql.cnpg.io "$instance" -o jsonpath='{.spec.imageName}{"\n"}'
kubectl -n "$NAMESPACE" get pods -l cnpg.io/cluster="$instance",cnpg.io/podRole=instance \
  -o jsonpath='{range .items[*]}{.metadata.uid}/{.status.containerStatuses[*].restartCount} {end}{"\n"}'
