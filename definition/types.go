// Package definition contains shared types used across the provider definition.
//
// +k8s:openapi-gen=true
package definition

// TopologyType defines the type of deployment topology.
type TopologyType string

// TopologyTypeReplicaSet is a primary with streaming replicas.
const TopologyTypeReplicaSet TopologyType = "replicaSet"

// Parameters defines instance-wide structured parameters that apply to the
// entire cluster (Instance.spec.parameters).
type Parameters struct {
	// TLS configures the certificate clients connect to. Connections are
	// always encrypted; this only chooses where the certificate comes from.
	// +optional
	TLS *TLSParameters `json:"tls,omitempty"`

	// Backup configures the operator's VolumeSnapshot backups.
	// +optional
	Backup *BackupParameters `json:"backup,omitempty"`

	// Bootstrap configures how a new instance gets its initial data.
	// It can only be set when the instance is created.
	// +optional
	Bootstrap *BootstrapParameters `json:"bootstrap,omitempty"`
}

// TLSParameters configures the gateway certificate. It comes from, in order
// of precedence: the certificate secret, the cert-manager issuer, or else a
// self-signed issuer the operator creates for the instance with cert-manager.
// The certificate secret and the issuer can only be set when the instance is
// created.
//
// +kubebuilder:validation:XValidation:rule="!(has(self.certificateSecretName) && size(self.certificateSecretName) > 0 && has(self.issuer) && has(self.issuer.name) && size(self.issuer.name) > 0)",message="set either certificateSecretName or issuer, not both"
// +kubebuilder:validation:XValidation:rule="!(has(self.certificateSecretName) && size(self.certificateSecretName) > 0 && has(self.additionalNames) && size(self.additionalNames) > 0)",message="additionalNames cannot be combined with certificateSecretName, whose certificate carries its own names"
type TLSParameters struct {
	// CertificateSecretName names a Secret in the instance namespace with
	// the certificate clients connect to: tls.crt and tls.key, and ca.crt
	// when clients should receive the CA. Cannot be combined with issuer or
	// additionalNames.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	CertificateSecretName string `json:"certificateSecretName,omitempty"`

	// Issuer is the cert-manager issuer that signs the certificate. It is
	// ignored without a name.
	// +optional
	Issuer *IssuerReference `json:"issuer,omitempty"`

	// AdditionalNames are DNS names the certificate is valid for besides
	// those of the gateway Service, such as an external name clients connect
	// to. Used with issuer or the self-signed certificate. Changing them
	// reissues the certificate, which the gateway loads without a restart.
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=253
	// +optional
	AdditionalNames []string `json:"additionalNames,omitempty"`
}

// IssuerKind is the kind of a cert-manager issuer.
type IssuerKind string

const (
	// IssuerKindIssuer is an Issuer in the instance namespace.
	IssuerKindIssuer IssuerKind = "Issuer"
	// IssuerKindClusterIssuer is a cluster-wide ClusterIssuer.
	IssuerKindClusterIssuer IssuerKind = "ClusterIssuer"
)

// CertManagerGroup is the API group of cert-manager's own issuers.
const CertManagerGroup = "cert-manager.io"

// IssuerReference refers to a cert-manager issuer.
type IssuerReference struct {
	// Name of the issuer.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	Name string `json:"name,omitempty"`

	// Kind of the issuer: Issuer or ClusterIssuer, or the kind of an
	// external issuer of group.
	// +kubebuilder:default=Issuer
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Kind IssuerKind `json:"kind,omitempty"`

	// Group is the API group of the issuer: cert-manager.io, or that of an
	// external issuer such as awspca.cert-manager.io.
	// +kubebuilder:default=cert-manager.io
	// +kubebuilder:validation:MaxLength=253
	// +optional
	Group string `json:"group,omitempty"`
}

// BackupParameters configures the backups the DocumentDB operator takes as
// CSI VolumeSnapshots of the primary's volume. They need a CSI driver with
// snapshot support and a default VolumeSnapshotClass, and stay in the cluster:
// they are not copied to object storage.
type BackupParameters struct {
	// Schedule is the cron expression backups are taken on, such as
	// "0 2 * * *". Leave it empty to take no scheduled backups.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Schedule string `json:"schedule,omitempty"`

	// RetentionDays is how long backups of the instance are kept.
	// The operator keeps them for 30 days when it is not set.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=365
	// +optional
	RetentionDays *int32 `json:"retentionDays,omitempty"`
}

// BootstrapParameters configures the initial data of a new instance.
type BootstrapParameters struct {
	// Backup restores the new instance from a backup.
	// +optional
	Backup *BootstrapBackup `json:"backup,omitempty"`
}

// BackupName returns the name of the backup the instance is restored from,
// empty when it starts empty. Forms submit the empty field as an empty name.
func (p Parameters) BackupName() string {
	if p.Bootstrap == nil || p.Bootstrap.Backup == nil {
		return ""
	}
	return p.Bootstrap.Backup.Name
}

// BootstrapBackup names the backup a new instance is restored from.
type BootstrapBackup struct {
	// Name of a completed documentdb.io Backup in the instance namespace.
	// Without a name the instance starts empty.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	Name string `json:"name,omitempty"`
}
