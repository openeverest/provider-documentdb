#!/bin/bash

# Environment variables sourced by the `make test-integration*` targets before
# running chainsaw.

export PROVIDER_ROOT_PATH=${PROVIDER_ROOT_PATH:-${PWD}}
echo "PROVIDER_ROOT_PATH=${PROVIDER_ROOT_PATH}"
