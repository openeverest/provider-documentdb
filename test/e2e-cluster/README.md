# End-to-end tests

These [chainsaw](https://kyverno.github.io/chainsaw/) suites run the provider
with the real DocumentDB operator and CloudNativePG, both installed by the
provider chart. They create OpenEverest `Instance`s, wait for the databases to
come up, and connect to them with `mongosh` through the `<instance>-conn`
Secret and its `ca.crt`, as a user would.

The [integration tests](../integration/README.md) cover the provider's mapping
quickly, without the operator; these cover what only the operator can tell:
that the database works.

## Suites

| Suite | Covers |
|---|---|
| `lifecycle/` | Create; connect (TLS verified, hostname included; plaintext refused); write and read data; scale 1→3→1; grow storage; change resources; upgrade 1.0.0→1.0.1; switch to LoadBalancer and back; delete. Asserts the DocumentDB image mode the operator chose. |
| `tls/` | The cert-manager Issuer TLS mode, with `additionalNames` (changing them reissues the certificate), and a certificate Secret the user brings. Deleting keeps the user's Secret. The default self-signed mode is in `lifecycle/`. |
| `upgrade/` | Upgrade from the previous chart release (`PREVIOUS_CHART_VERSION`) with a database running. The pre-upgrade hook blocks while an Instance is on an engine version the new release lacks, leaving the old provider in place; then the upgrade goes through, the hook applies the operator CRDs, and the database keeps its data and pods. Deploy with `make deploy-provider-previous` instead of `deploy-provider-e2e`. |
| `operator-upgrade/` | Upgrade the DocumentDB operator (`PREVIOUS_OPERATOR_VERSION` to `DOCUMENTDB_OPERATOR_VERSION`) under a database pinned to an engine version neither release defaults to. The database stays on its version and its PostgreSQL image, keeps its data, and its pods aren't restarted. Deploy with `make deploy-provider-operator-previous`, which installs the operator on its own and the provider without one. |

`_lib/` holds the `mongosh` client pod and the scripts the steps share:
`mongosh.sh` runs JavaScript on an instance, `data.sh` writes and checks a
known document set, `image.sh` checks the image mode and version on the
CloudNativePG `Cluster` and its pods.

## Image modes

The operator runs DocumentDB either as an ImageVolume extension of the
PostgreSQL image, or as a combined PostgreSQL + DocumentDB image. Its mode
(the chart's `operator.imageMode`) applies to the whole operator, so CI runs
`lifecycle/` twice, on separate clusters: with `auto`, which picks ImageVolume
on k3s 1.35, and with `combined`. `E2E_IMAGE_MODE` (`imageVolume` or
`combined`) makes `image.sh` fail when the operator chose the other mode;
unset, it accepts either.

## Running locally

The cluster needs cert-manager, a default StorageClass that can grow volumes,
and a load balancer. `test/e2e-cluster/k3d_config.yaml` is a single-node k3s
1.35 cluster that keeps servicelb; `install-csi-hostpath` replaces local-path,
which can't grow volumes, with the CSI hostpath driver.

```bash
make k3d-cluster-up K3D_CONFIG=./test/e2e-cluster/k3d_config.yaml
make install-csi-hostpath deploy-cert-manager
make docker-build load-image install-core-crds   # the chart brings the operator CRDs
make deploy-provider-e2e                     # DOCUMENTDB_IMAGE_MODE=combined for the other mode
# OpenEverest core, from a checkout of openeverest/openeverest:
#   make build-controller docker-build-controller, then here
#   make load-openeverest-controller-image, then there
#   make deploy-test-controller wait-test-controller
make test-e2e-cluster                        # lifecycle and tls; or test-e2e-cluster-lifecycle, test-e2e-cluster-tls
```

The upgrade suite starts from the released chart: deploy it with
`make deploy-provider-previous` (instead of `deploy-provider-e2e`), then run
`make test-e2e-cluster-upgrade`; the suite upgrades to `IMG` itself. Run it on
its own cluster, or redeploy the previous release before it.

The operator-upgrade suite likewise starts from `make
deploy-provider-operator-previous`; run `make
test-e2e-cluster-operator-upgrade`, and the suite upgrades the operator with
`make deploy-operator`. When the DocumentDB operator ships a release, bump
`DOCUMENTDB_OPERATOR_VERSION` (and the chart dependency) and set
`PREVIOUS_OPERATOR_VERSION` to the release it replaces.

Any cluster with those pieces works, for example a cloud one with its own
StorageClass and load balancers; push the image to a registry its nodes reach
and set `PROVIDER_IMAGE_PULL_POLICY=Always` instead of `make load-image`. Volumes there may have a minimum size: the
tests use 10Gi, grown to 12Gi.
