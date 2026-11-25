#!/bin/sh
# Usage: data.sh write|check <instance> [mongosh options...]
#
# write upserts three documents into e2e.items; check fails unless exactly
# those three documents are there.
set -eu

action=$1
instance=$2
shift 2
dir=$(dirname "$0")

case "$action" in
  write)
    sh "$dir/mongosh.sh" "$instance" \
      'const c = db.getSiblingDB("e2e").items; for (const i of [1, 2, 3]) c.updateOne({_id: i}, {$set: {v: i}}, {upsert: true}); print("written")' "$@"
    ;;
  check)
    got=$(sh "$dir/mongosh.sh" "$instance" \
      'const d = db.getSiblingDB("e2e").items.find().sort({_id: 1}).toArray(); print(d.map(x => x._id + "=" + x.v).join(","))' "$@" | tail -n 1)
    echo "e2e.items: $got"
    test "$got" = "1=1,2=2,3=3"
    ;;
  *)
    echo "unknown action $action" >&2
    exit 1
    ;;
esac
