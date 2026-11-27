# DocumentDB Provider

> [!WARNING]
> **Pre-alpha.** OpenEverest v2 and this provider are under active development. CRD schemas,
> chart values and defaults change frequently, including in breaking ways, and there is no
> supported upgrade path between versions yet. Not for production use.

<!-- TODO(sdk): remove the pre-alpha banner and the status badge at v2 GA. -->

[![Status](https://img.shields.io/badge/status-pre--alpha-orange)](https://github.com/openeverest/openeverest)
[![CI](https://github.com/openeverest/provider-documentdb/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/openeverest/provider-documentdb/actions/workflows/ci.yaml)
[![Release](https://img.shields.io/github/v/release/openeverest/provider-documentdb)](https://github.com/openeverest/provider-documentdb/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/openeverest/provider-documentdb.svg)](https://pkg.go.dev/github.com/openeverest/provider-documentdb)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)

Run **[DocumentDB](https://documentdb.io/)**, the MongoDB-compatible document database built on
PostgreSQL, on Kubernetes through [OpenEverest](https://github.com/openeverest/openeverest),
backed by the [DocumentDB Kubernetes Operator](https://github.com/documentdb/documentdb-kubernetes-operator).

## What this is

OpenEverest providers translate a single, technology-agnostic `Instance` custom resource into
the native custom resources of an upstream Kubernetes operator — for databases, but equally
for caches, message queues, object storage, or model-serving runtimes. This repository is the
provider for DocumentDB: it owns the technology-specific knowledge — topologies, versions,
parameters, backup wiring — so that users, the API server, and the UI stay technology-agnostic.

> [!IMPORTANT]
> **This provider is not standalone.** It requires an OpenEverest installation (core CRDs and
> controller) in the cluster. Installing this chart on its own does nothing.
> See [Install OpenEverest](https://openeverest.io/documentation/current/quick-install.html).

```mermaid
flowchart LR
    U([User / API / UI]) -->|creates| I["Instance<br/>core.openeverest.io"]
    I --> P["provider-documentdb<br/>(this repository)"]
    P -->|reconciles into| O["Operator CR<br/>documentdb.io"]
    O --> W["Upstream operator"]
    W --> R[("Workloads, Services,<br/>Secrets, PVCs")]
    P -->|status, endpoints,<br/>credentials| I
```

The provider watches `Instance` resources whose `spec.providerRef.name` is
`documentdb`, and reports workload health back onto `Instance.status`. It never
manages pods directly — all lifecycle work is delegated to the operator.

## Compatibility

| provider-documentdb | OpenEverest | DocumentDB operator | DocumentDB | Kubernetes |
|---|---|---|---|---|
| `0.1.x` | `2.0.0-alpha.1` | `1.2.1` (bundles CloudNativePG `1.29.1`; runs on CloudNativePG `1.29` or `1.30`) | `1.0.0`, `1.0.1` | `1.32` or later |

[cert-manager](https://cert-manager.io/) is required: the operator issues its webhook and
gateway certificates with it.

## Capabilities

What you can do to a running instance through the `Instance` API. Upgrading the
provider itself is covered under [Installation](#installation).

| Capability | Status | Notes |
|---|---|---|
| Provisioning | ✅ | One topology, `replicaSet`: a primary and up to two streaming replicas |
| Horizontal scaling | ✅ | 1 to 3 instances. 3 is the default; a replica is promoted when the primary fails |
| Vertical scaling (CPU / memory) | ✅ | `resources.limits` is the pod's envelope; the operator splits it between PostgreSQL and the gateway and runs them with requests equal to limits. Rolling restart: waits for [maintenance approval](#maintenance) |
| Version upgrades | ✅ | Change `spec.version`; the extension schema follows the binary (`schemaVersion: auto`). Downgrades are refused by the operator |
| Monitoring | ✅ | Prometheus metrics for ExtensionManaged monitoring classes, see [Monitoring](#monitoring). Adding the first destination or removing the last one rolls the pods and waits for [maintenance approval](#maintenance) |
| Scheduling policy | ✅ | `schedulingPolicy`: node selector, tolerations and affinity. Rolling restart: waits for [maintenance approval](#maintenance) |
| TLS | ✅ | Always on: the gateway refuses unencrypted connections. See [TLS](#tls) |

Stateful workloads additionally report:

| Capability | Status | Notes |
|---|---|---|
| Persistent storage | ✅ | |
| Storage expansion | ✅ | Grow only, when the storage class allows it |
| Backups (on demand) | ⚠️ | The operator's VolumeSnapshot backups, as `documentdb.io` `Backup` resources. Not OpenEverest `Backup`s, see [Backups and restores](#backups-and-restores) |
| Backups (scheduled) | ⚠️ | `parameters.backup`, same caveat |
| Restore | ⚠️ | Into a new instance, with `parameters.bootstrap.backup.name` |

Not supported, because the operator does not offer them: point-in-time recovery, backups to
object storage, restores in place, `NodePort` services, more than three instances and
sharding. Custom PostgreSQL configuration and multi-region replication are not wired yet.

## Installation

Prerequisites: OpenEverest and [cert-manager](https://cert-manager.io/docs/installation/).

The provider chart is published as an OCI artifact:

```bash
helm install provider-documentdb \
  oci://ghcr.io/openeverest/charts/provider-documentdb \
  --version <chart-version> \
  --namespace everest-system
```

The DocumentDB operator is bundled as a chart dependency and is installed automatically,
together with the CloudNativePG operator it builds on:

- CloudNativePG goes to the `cnpg-system` namespace. If it is already installed (for example
  by `provider-cloudnative-pg`), the install fails and tells you the namespace to use; run the
  DocumentDB operator on the existing CloudNativePG instead:

  ```bash
  helm install provider-documentdb \
    oci://ghcr.io/openeverest/charts/provider-documentdb \
    --version <chart-version> \
    --namespace everest-system \
    --set operator.cloudnative-pg.enabled=false \
    --set operator.cloudnative-pg.namespaceOverride=everest-system
  ```

  `namespaceOverride` is the CloudNativePG namespace (`everest-system` for
  `provider-cloudnative-pg`): the DocumentDB sidecar plugin is installed there, where
  CloudNativePG looks for plugins. The `Provider` reports whether that CloudNativePG is one the
  DocumentDB operator supports, in its `CloudNativePGCompatible` condition:

  ```bash
  kubectl get provider documentdb -o jsonpath='{.status.conditions[?(@.type=="CloudNativePGCompatible")]}'
  ```

  Installed the other way round, `provider-cloudnative-pg` needs `cloudnative-pg.enabled=false`
  to run on the bundled CloudNativePG.
- For backups, install the VolumeSnapshot CRDs and a snapshot-capable CSI driver before the
  chart. CloudNativePG looks for the CRDs when it starts: if you add them later, restart it.
- Set `operator.enabled=false` to manage the DocumentDB operator yourself.

Upgrade and uninstall:

```bash
helm upgrade provider-documentdb oci://ghcr.io/openeverest/charts/provider-documentdb
helm uninstall provider-documentdb --namespace everest-system
```

`helm upgrade` runs a pre-upgrade hook first: a Job with the new provider image checks the
live Instances against the new release (for example, an engine version it drops) and aborts
the upgrade, changing nothing, if any would be left unsupported. Its log lists them:
`kubectl -n <namespace> logs job/provider-documentdb-preflight`. When the check passes, the
Job applies the bundled DocumentDB operator's CRDs, which Helm installs but never upgrades,
so the new operator starts with the CRDs it needs. CloudNativePG's CRDs are chart templates
that Helm upgrades itself. With `operator.enabled=false` the hook only runs the check.

### Operator upgrades

A new DocumentDB or CloudNativePG operator, whether it comes with a provider release or
you manage it yourself, keeps running instances on their images:

- The DocumentDB extension and gateway images follow `spec.version`, which the provider
  always sets. A new operator's default DocumentDB version applies to nothing.
- The PostgreSQL image (ImageVolume mode) is the provider's `postgresImage` value,
  `ghcr.io/cloudnative-pg/postgresql:18.3-system-trixie` by default, not the operator's
  `POSTGRES_IMAGE` setting or CloudNativePG's built-in default, which change between
  releases. When it changes for a running instance (a provider release with a new default,
  or a new `postgresImage`), the provider holds the change and asks for a maintenance window:
  the Instance lists it in `status.pendingMaintenance` as a `RollingRestart` action with a
  `postgres-image-…` token. Approve it with `spec.maintenance.approved: <token>`, or let it
  through with `spec.maintenance.autoApproveUpTo: RollingRestart`. Until then the instance
  stays on its image. In combined image mode the combined image follows `spec.version`, and
  `postgresImage` is not used.
- CloudNativePG updates its instance manager in place, so upgrading it does not restart the
  database pods.

What the provider can't hold, and may restart pods when an operator release changes it: the
operator's OpenTelemetry collector image (when monitoring is enabled), and the gateway
sidecar's settings that the operator restarts pods for itself. The upgrade from operator
1.2.0 to 1.2.1 restarts nothing; the `operator-upgrade` e2e suite checks each new operator
release.

Uninstalling the chart does **not** delete running `Instance` resources or their data.

## Usage

Verify that the provider registered itself:

```bash
kubectl get providers.core.openeverest.io documentdb
```

Create an instance:

```yaml
apiVersion: core.openeverest.io/v1alpha1
kind: Instance
metadata:
  name: my-instance
spec:
  providerRef:
    name: documentdb
  components:
    engine:
      replicas: 3
      resources:
        limits:
          cpu: "1"
          memory: 2Gi
      storage:
        size: 10Gi
```

The only component is `engine` — see [definition/provider.yaml](definition/provider.yaml).
`spec.version` and `spec.topology` are optional; the provider defaults apply.
More examples live in [examples/](examples/).

Watch it come up and read the connection details:

```bash
kubectl get instance my-instance -w
kubectl get instance my-instance -o jsonpath='{.status.connectionSecretRef.name}'
```

Connection details are in the Secret named by `.status.connectionSecretRef.name`: `host`,
`port`, `username`, `password`, a MongoDB `uri` and, in `ca.crt`, the CA that signed the
gateway certificate. Any MongoDB driver or tool connects with them:

```bash
kubectl get secret my-instance-conn -o jsonpath='{.data.ca\.crt}' | base64 -d > ca.crt
mongosh "$(kubectl get secret my-instance-conn -o jsonpath='{.data.uri}' | base64 -d)" --tlsCAFile ca.crt
```

The administrator is generated (`everest`, in the Secret `<instance>-credentials`) unless
`spec.userSecretRef` names a Secret with `username` and `password`. The gateway creates the
user when the instance is initialized, so `userSecretRef` can only be set when the Instance
is created.

### TLS

The gateway only accepts TLS connections. `spec.parameters.tls` chooses the certificate:

| Setting | Certificate |
|---|---|
| none | Issued by a self-signed issuer the operator creates for the instance with cert-manager |
| `issuer: {name, kind, group}` | Issued by your cert-manager `Issuer` or `ClusterIssuer` (`kind` defaults to `Issuer`), or by an external issuer of `group`, such as `awspca.cert-manager.io` |
| `certificateSecretName` | The `tls.crt` and `tls.key` of your Secret, and its `ca.crt` for clients |

Issued certificates cover the names of the gateway Service
(`documentdb-service-<instance>`, `.<namespace>` and `.<namespace>.svc`) and
`additionalNames`, the DNS names clients reach the instance at otherwise, such as that of a
`LoadBalancer`. Without one of them, connect through a `LoadBalancer` address with
`tlsAllowInvalidHostnames=true`. `additionalNames` can't be combined with
`certificateSecretName`, whose certificate carries its own names, and must be DNS names,
not IP addresses.

`certificateSecretName` and `issuer` can only be set when the instance is created.
`additionalNames` can be changed at any time: cert-manager reissues the certificate and
the gateway loads it without a restart, within a couple of minutes, once the kubelet has
updated the mounted Secret. The `ca.crt` of the connection Secret follows the
certificate. The UI sets the issuer and the certificate Secret; `additionalNames` is set
in YAML or through the API.

#### Bring your own CA

To have clients verify `mydb.company.com` against your company CA:

1. Give cert-manager the CA, for example as a `ClusterIssuer` of the CA's key pair:

   ```bash
   kubectl -n cert-manager create secret tls company-ca --cert=ca.crt --key=ca.key
   kubectl apply -f - <<'YAML'
   apiVersion: cert-manager.io/v1
   kind: ClusterIssuer
   metadata:
     name: company-ca
   spec:
     ca:
       secretName: company-ca
   YAML
   ```

2. Create the instance with it:

   ```yaml
   spec:
     parameters:
       tls:
         issuer:
           name: company-ca
           kind: ClusterIssuer
         additionalNames:
           - mydb.company.com
   ```

3. Point `mydb.company.com` at the gateway: a `LoadBalancer` address
   (`spec.components.engine.service.serviceType: LoadBalancer`), or your ingress. Clients
   then verify the certificate with the company CA, also in the connection Secret's `ca.crt`:

   ```bash
   mongosh "mongodb://<user>:<password>@mydb.company.com:10260/?tls=true&directConnection=true&authMechanism=SCRAM-SHA-256" \
     --tlsCAFile company-ca.crt
   ```

### Monitoring

Add a `MonitoringDestination` of an ExtensionManaged class (for example
[monitoring-prometheus](https://github.com/openeverest/monitoring-prometheus)) to
`spec.monitoring.destinations`. The provider then publishes two scrape endpoints per pod:

- port `metrics` (9187): PostgreSQL metrics of the CloudNativePG instance manager;
- port `prom-metrics` (9188): the DocumentDB metrics of the operator's OpenTelemetry
  Collector sidecar. The provider adds the sidecar while a destination requires metrics,
  which restarts the pods one at a time, so adding or removing it waits for
  [maintenance approval](#maintenance).

### Maintenance

Changes that restart the pods are held until you approve them. The
DocumentDB keeps running with the current settings, the Instance reports
`MaintenancePending` and lists the change in `status.pendingMaintenance`:

| Change | Impact |
|---|---|
| CPU or memory (`resources.limits`) | Rolling restart |
| Scheduling policy (`schedulingPolicy`) | Rolling restart |
| Adding the first or removing the last metrics destination (collector sidecar) | Rolling restart |

Everything else applies at once: replicas, storage growth, backups, the Service type and
`tls.additionalNames` (the gateway reloads the certificate). The certificate source cannot be
changed after creation.

Approve one change by copying its token, or approve every rolling restart for the instance:

```bash
kubectl get instance my-instance -o jsonpath='{.status.pendingMaintenance}'
kubectl patch instance my-instance --type=merge \
  -p '{"spec":{"maintenance":{"approved":"<approvalToken>"}}}'
# or, standing approval:
kubectl patch instance my-instance --type=merge \
  -p '{"spec":{"maintenance":{"autoApproveUpTo":"RollingRestart"}}}'
```

The token names one change: changing the same setting again needs a new approval.

### Backups and restores

The operator backs an instance up as a CSI
[VolumeSnapshot](https://kubernetes.io/docs/concepts/storage/volume-snapshots/) of the
primary's volume. That needs a CSI driver with snapshot support and a default
`VolumeSnapshotClass`, and the backups stay in the cluster.

OpenEverest's own backup API writes to object storage (`BackupStorage`), which the operator
cannot do, so these backups are **not** OpenEverest `Backup` resources and do not show in the
UI's backup pages; `spec.backup` and `spec.dataSource` are rejected. They are configured
with instance parameters instead:

```yaml
spec:
  parameters:
    backup:
      schedule: "0 2 * * *"   # cron; the provider creates a documentdb.io ScheduledBackup
      retentionDays: 14       # 30 when omitted
```

Take a backup on demand with the operator's resource:

```yaml
apiVersion: documentdb.io/v1
kind: Backup
metadata:
  name: my-backup
spec:
  cluster:
    name: my-instance
```

Restore by creating a new instance from a completed backup in the same namespace:

```yaml
spec:
  parameters:
    bootstrap:
      backup:
        name: my-backup
```

The users are part of the data, so the restored instance opens with the credentials of the
instance the backup was taken from. The provider copies them while that instance exists.
Backups outlive their instance until their retention expires, so a deleted instance can be
restored too, but its generated credentials are deleted with it: keep them, and create the
new instance with `spec.userSecretRef` naming a Secret with that `username` and `password`.

Whoever may create an Instance in a namespace can restore any backup in it, and with it
gets the credentials of the instance it was taken from.

## Topologies

<!-- BEGIN GENERATED: topologies -->
| Topology | Default | Description |
|---|---|---|
| `replicaSet` | ✅ | One primary and up to two streaming replicas, with automatic failover |
<!-- END GENERATED: topologies -->

## Versions

<!-- BEGIN GENERATED: versions -->
| Version bundle | Default | DocumentDB |
|---|---|---|
| `1.0.1` | ✅ | `1.0.1` |
| `1.0.0` | | `1.0.0` |
<!-- END GENERATED: versions -->

Source of truth: [definition/versions.yaml](definition/versions.yaml).

Upgrades go forward only: once the extension schema is updated, the operator refuses an
older version.

## Configuration

- **Chart values:** [charts/provider-documentdb/values.yaml](charts/provider-documentdb/values.yaml)
- **Instance parameters:** `spec.parameters` (`tls`, `backup`, `bootstrap`), defined in
  [definition/types.go](definition/types.go) and published on the `Provider` resource
  (`kubectl get provider documentdb -o yaml`). The provider validates every
  Instance against these schemas, and the UI checks the form against them as you type.
- **Admission webhook:** the chart registers one, so an invalid Instance is rejected when it
  is written. Its serving certificate is generated by the chart unless you set
  `admission.certs` or `admission.certManager.enabled`.
- **Deleting an Instance deletes its data.** The provider sets the operator's
  `persistentVolumeReclaimPolicy` to `Delete`; the operator's own default keeps the volumes.

## Development

Requires Go (see [go.mod](go.mod)), Docker, Helm, kubectl, and a Kubernetes cluster you can
reach. [dev/README.md](dev/README.md) covers the environment end to end: the recommended
local k3d setup, running against a cluster you already have, and every `dev/.env` setting.

```bash
make dev-up             # local cluster + Tilt dev environment (see dev/README.md)
make generate           # RBAC, operator CRDs, provider spec, Helm chart sync
make run                # run the provider locally against the cluster
make test-unit
make test-integration   # chainsaw suites under test/integration/
make test-e2e-cluster   # chainsaw suites under test/e2e-cluster/, with the real operator
make dev-down
```

`make help` lists every target. `make verify` fails when generated files are stale — run
`make generate` and commit the result.

The provider contract (`Validate` / `Sync` / `Status` / `Cleanup`), RBAC markers, watches,
code generation, and the backup/restore interfaces are documented once for all providers in
[PROVIDER_DEVELOPMENT.md](https://github.com/openeverest/provider-sdk/blob/main/PROVIDER_DEVELOPMENT.md).

### Layout

| Path | Purpose |
|---|---|
| `cmd/provider/` | Entry point |
| `internal/provider/` | `ProviderInterface` implementation, backup interfaces, RBAC markers |
| `internal/common/` | Component name constants |
| `internal/operatorcrds/` | The DocumentDB operator's CRDs the pre-upgrade hook applies (`crd.yaml` is copied by `make generate` from the operator module in `go.mod`) |
| `definition/` | Provider identity, component types, versions, topologies, backup classes, monitoring contract |
| `charts/provider-documentdb/` | Helm chart (`generated/` is produced by `make generate`) |
| `config/rbac/role.yaml` | Generated `ClusterRole` — do not edit |
| `test/integration/` | Chainsaw suites without the operator (see its `README.md`) |
| `test/e2e-cluster/` | Chainsaw suites with the real operator (see its `README.md`) |
| `test/vars.sh` | Environment of the chainsaw tests |
| `examples/` | Example `Instance` resources |
| `dev/` | Tilt dev environment, `.env` configuration, k3d cluster config |
| `.github/workflows/` | CI: lint, build, unit, integration and e2e-cluster tests, release |
| `.github/dependabot.yml` | Daily PRs for new operator releases (Go module + Helm subchart) |

### Testing

- **Unit tests** — `make test-unit`.
- **Integration tests** — chainsaw suites under `test/integration/`. They run without the
  DocumentDB operator and play its part by patching statuses. See
  [test/integration/README.md](test/integration/README.md).
- **End-to-end tests** — chainsaw suites under `test/e2e-cluster/`. They run the real
  DocumentDB operator and CloudNativePG, and connect with `mongosh`. See
  [test/e2e-cluster/README.md](test/e2e-cluster/README.md).
- **CI** — `.github/workflows/ci.yaml` runs lint, build, unit tests, generated-file
  verification, Helm lint (and that the embedded operator CRDs match the bundled operator
  chart), each integration suite, and the e2e-cluster suites (lifecycle in both image modes,
  TLS, upgrade from the previous release) on every pull request.

## Troubleshooting

```bash
kubectl logs -n everest-system deploy/provider-documentdb -f
```

| Symptom | Where to look |
|---|---|
| `Instance` stuck in `Creating` | `kubectl describe instance <name>` conditions, then the provider logs |
| No `Provider` resource in the cluster | Is the chart installed? Check the provider deployment logs |
| `Instance` ignored entirely | `spec.providerRef.name` must be `documentdb` |
| Operator resource created but no pods | `kubectl describe documentdb <name>` and the `clusters.postgresql.cnpg.io` of the same name — the failure is upstream |
| Instance Ready but `status.components` empty, no metrics | The `Provider`'s `CloudNativePGCompatible` condition: `kubectl get provider documentdb -o jsonpath='{.status.conditions}'` (see [Installation](#installation)) |
| Backups fail with `missing VolumeSnapshot CRD` | Install the VolumeSnapshot CRDs, then restart the CloudNativePG operator |
| Backups stay `pending` or fail | The storage class needs a CSI driver with snapshot support and a default `VolumeSnapshotClass` |

## Contributing

Issues and pull requests are welcome. See
[PROVIDER_DEVELOPMENT.md](https://github.com/openeverest/provider-sdk/blob/main/PROVIDER_DEVELOPMENT.md)
and the [OpenEverest Code of Conduct](https://github.com/openeverest/openeverest/blob/main/CODE_OF_CONDUCT.md).

## Security

Report vulnerabilities per the
[OpenEverest security policy](https://github.com/openeverest/openeverest/blob/main/SECURITY.md).
Please do not open public issues for security reports.

## License

Apache License 2.0 — see [LICENSE](LICENSE) for details.
