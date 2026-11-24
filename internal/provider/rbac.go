package provider

// Run `make manifests` to regenerate config/rbac/role.yaml from these markers.
// This file contains kubebuilder RBAC markers for controller-gen.
// See: https://book.kubebuilder.io/reference/markers/rbac

// Base RBAC (required by all providers):
// +kubebuilder:rbac:groups=core.openeverest.io,resources=instances,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=core.openeverest.io,resources=instances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.openeverest.io,resources=instances/finalizers,verbs=update
// +kubebuilder:rbac:groups=core.openeverest.io,resources=providers,verbs=get;list;watch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// The runtime records validation warnings as events.k8s.io Events.
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
// The runtime writes the connection details returned by Status() into a Secret.
// The provider generates the credentials Secret and reads the gateway CA.
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// DocumentDB operator resources.
// +kubebuilder:rbac:groups=documentdb.io,resources=dbs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=documentdb.io,resources=dbs/status,verbs=get
// +kubebuilder:rbac:groups=documentdb.io,resources=scheduledbackups,verbs=get;list;watch;create;update;patch;delete
// The source backup of a restore is read to check it belongs to the namespace.
// +kubebuilder:rbac:groups=documentdb.io,resources=backups,verbs=get;list;watch

// The CloudNativePG Cluster the operator creates is read for the ready
// instances and labelled so its pods inherit the OpenEverest labels.
// +kubebuilder:rbac:groups=postgresql.cnpg.io,resources=clusters,verbs=get;list;watch;patch

// The provider keeps the names of the gateway certificate the operator
// requests up to date with spec.parameters.tls.additionalNames.
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get;patch

// The gateway Service carries the load balancer address.
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
// The runtime counts the labelled pods into status.components.
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// Monitoring: the runtime resolves the Instance's bindings and reports them.
// +kubebuilder:rbac:groups=monitoring.openeverest.io,resources=monitoringclasses;monitoringdestinations;monitoringbindings,verbs=get;list;watch
// +kubebuilder:rbac:groups=monitoring.openeverest.io,resources=monitoringbindings/status,verbs=patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get
