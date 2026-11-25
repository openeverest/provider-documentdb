#!/bin/sh
# Usage: mongosh.sh <instance> <javascript> [mongosh options...]
#
# Runs <javascript> in mongosh against an Instance the way a client would: from
# the mongosh Pod, with the uri and ca.crt of the Secret the Instance names in
# status.connectionSecretRef. Prints the result. The gateway may be restarting
# or switching primaries after a change, so failures are retried for a while;
# keep writes idempotent.
set -eu

instance=$1
script=$2
shift 2

secret=$(kubectl -n "$NAMESPACE" get instance "$instance" -o jsonpath='{.status.connectionSecretRef.name}')
test -n "$secret"
uri=$(kubectl -n "$NAMESPACE" get secret "$secret" -o jsonpath='{.data.uri}' | base64 -d)
kubectl -n "$NAMESPACE" get secret "$secret" -o jsonpath='{.data.ca\.crt}' | base64 -d \
  | kubectl -n "$NAMESPACE" exec -i mongosh -- sh -c "cat > /tmp/$instance-ca.crt"

attempt=1
until kubectl -n "$NAMESPACE" exec mongosh -- \
  mongosh --quiet "$uri" --tlsCAFile "/tmp/$instance-ca.crt" "$@" --eval "$script"; do
  if [ "$attempt" -ge "${MONGOSH_ATTEMPTS:-20}" ]; then
    echo "mongosh failed $attempt times against $instance" >&2
    exit 1
  fi
  attempt=$((attempt + 1))
  sleep 6
done
