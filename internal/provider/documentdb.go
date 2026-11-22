package provider

import (
	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	corev1 "k8s.io/api/core/v1"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-documentdb/definition"
	"github.com/openeverest/provider-documentdb/internal/common"
)

const (
	// defaultReplicas matches the topology default: a primary and two
	// replicas, the operator's recommendation for high availability.
	defaultReplicas = 3

	// schemaVersionAuto has the operator update the extension schema with
	// the binary, so changing the Instance version is the whole upgrade.
	schemaVersionAuto = "auto"

	tlsModeSelfSigned  = "SelfSigned"
	tlsModeCertManager = "CertManager"
	tlsModeProvided    = "Provided"

	certManagerGroup = "cert-manager.io"

	reclaimPolicyDelete = "Delete"
)

// decodeParameters returns the Instance parameters, empty when none are set.
// The runtime has already checked them against the published schema.
func decodeParameters(c *controller.Context) definition.Parameters {
	var params definition.Parameters
	c.TryDecodeParameters(&params)
	return params
}

// buildDocumentDB renders the whole DocumentDB the Instance asks for.
func buildDocumentDB(
	c *controller.Context, params definition.Parameters, credentialsSecret string, monitoring *dbv1.MonitoringSpec,
) *dbv1.DocumentDB {
	engine := c.Instance().Spec.Components[common.ComponentEngine]

	db := &dbv1.DocumentDB{
		ObjectMeta: c.ObjectMeta(c.Name()),
		Spec: dbv1.DocumentDBSpec{
			// The operator supports one node; replicas are instances of it.
			NodeCount:                  1,
			InstancesPerNode:           int(engineReplicas(engine)),
			DocumentDBVersion:          engine.Version,
			SchemaVersion:              schemaVersionAuto,
			DocumentDbCredentialSecret: credentialsSecret,
			Resource:                   buildResource(engine),
			ExposeViaService:           dbv1.ExposeViaService{ServiceType: string(serviceType(engine))},
			Affinity:                   buildAffinity(engine),
			TLS:                        &dbv1.TLSConfiguration{Gateway: buildGatewayTLS(params.TLS)},
			Monitoring:                 monitoring,
		},
	}

	if params.Backup != nil && params.Backup.RetentionDays != nil {
		db.Spec.Backup = &dbv1.BackupConfiguration{RetentionDays: int(*params.Backup.RetentionDays)}
	}
	if params.Bootstrap != nil && params.Bootstrap.Backup != nil {
		db.Spec.Bootstrap = &dbv1.BootstrapConfiguration{
			Recovery: &dbv1.RecoveryConfiguration{
				Backup: cnpgv1.LocalObjectReference{Name: params.Bootstrap.Backup.Name},
			},
		}
	}
	return db
}

func engineReplicas(engine corev1alpha1.ComponentSpec) int32 {
	if engine.Replicas == nil {
		return defaultReplicas
	}
	return *engine.Replicas
}

func serviceType(engine corev1alpha1.ComponentSpec) corev1.ServiceType {
	if engine.Service == nil || engine.Service.ServiceType == "" {
		return corev1.ServiceTypeClusterIP
	}
	return engine.Service.ServiceType
}

// buildResource renders storage and the pod's CPU and memory envelope. The
// operator splits the envelope between PostgreSQL, the gateway and the
// collector sidecar, and runs them with requests equal to limits.
func buildResource(engine corev1alpha1.ComponentSpec) dbv1.Resource {
	res := dbv1.Resource{
		Storage: dbv1.StorageConfiguration{
			// Deleting the Instance deletes its data, as with every provider.
			// The operator's default would keep the volumes behind.
			PersistentVolumeReclaimPolicy: reclaimPolicyDelete,
		},
	}
	if engine.Storage != nil {
		res.Storage.PvcSize = engine.Storage.Size.String()
		if engine.Storage.StorageClass != nil {
			res.Storage.StorageClass = *engine.Storage.StorageClass
		}
	}
	if engine.Resources != nil {
		if cpu, ok := engine.Resources.Limits[corev1.ResourceCPU]; ok {
			res.CPU = cpu.String()
		}
		if memory, ok := engine.Resources.Limits[corev1.ResourceMemory]; ok {
			res.Memory = memory.String()
		}
	}
	return res
}

// buildAffinity renders the scheduling policy. Without an affinity the
// operator's default applies, which prefers a different node per instance;
// an affinity, even an empty one, replaces it.
func buildAffinity(engine corev1alpha1.ComponentSpec) cnpgv1.AffinityConfiguration {
	var affinity cnpgv1.AffinityConfiguration
	policy := engine.SchedulingPolicy
	if policy == nil {
		return affinity
	}
	affinity.NodeSelector = policy.NodeSelector
	affinity.Tolerations = policy.Tolerations
	if policy.Affinity != nil {
		disabled := false
		affinity.EnablePodAntiAffinity = &disabled
		affinity.NodeAffinity = policy.Affinity.NodeAffinity
		affinity.AdditionalPodAffinity = policy.Affinity.PodAffinity
		affinity.AdditionalPodAntiAffinity = policy.Affinity.PodAntiAffinity
	}
	return affinity
}

// buildGatewayTLS chooses where the gateway certificate comes from. The
// gateway only accepts TLS connections whichever mode is chosen.
func buildGatewayTLS(tls *definition.TLSParameters) *dbv1.GatewayTLS {
	switch {
	case tls != nil && tls.CertificateSecretName != "":
		return &dbv1.GatewayTLS{
			Mode:     tlsModeProvided,
			Provided: &dbv1.ProvidedTLS{SecretName: tls.CertificateSecretName},
		}
	case tls != nil && tls.Issuer != nil:
		kind := tls.Issuer.Kind
		if kind == "" {
			kind = definition.IssuerKindIssuer
		}
		return &dbv1.GatewayTLS{
			Mode: tlsModeCertManager,
			CertManager: &dbv1.CertManagerTLS{
				IssuerRef: dbv1.IssuerRef{Name: tls.Issuer.Name, Kind: string(kind), Group: certManagerGroup},
			},
		}
	default:
		return &dbv1.GatewayTLS{Mode: tlsModeSelfSigned}
	}
}
