package provider

import (
	"context"
	"testing"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller/validationtest"

	"github.com/openeverest/provider-documentdb/definition"
	"github.com/openeverest/provider-documentdb/internal/common"
)

func render(t *testing.T, fixture string, monitoring *dbv1.MonitoringSpec) *dbv1.DocumentDB {
	t.Helper()
	in := validationtest.Load(t, "testdata/validate/"+fixture)
	in.Namespace = "ns"
	// The runtime resolves the version bundle before Sync.
	engine := in.Spec.Components[common.ComponentEngine]
	engine.Version = "1.0.1"
	in.Spec.Components[common.ComponentEngine] = engine

	c := controller.NewContext(context.Background(), nil, in, common.ProviderName)
	return buildDocumentDB(c, decodeParameters(c), credentialsSecretName(c), monitoring)
}

func TestBuildDocumentDBDefaults(t *testing.T) {
	t.Parallel()
	db := render(t, "valid.yaml", nil)

	assert.Equal(t, "example", db.Name)
	assert.Equal(t, 1, db.Spec.NodeCount)
	assert.Equal(t, 3, db.Spec.InstancesPerNode)
	assert.Equal(t, "1.0.1", db.Spec.DocumentDBVersion)
	assert.Equal(t, "auto", db.Spec.SchemaVersion)
	assert.Equal(t, "example-credentials", db.Spec.DocumentDbCredentialSecret)
	assert.Equal(t, dbv1.Resource{
		CPU:    "1",
		Memory: "2Gi",
		Storage: dbv1.StorageConfiguration{
			PvcSize:                       "10Gi",
			PersistentVolumeReclaimPolicy: "Delete",
		},
	}, db.Spec.Resource)
	assert.Equal(t, "ClusterIP", db.Spec.ExposeViaService.ServiceType)
	// Connections are encrypted without the user asking for it.
	assert.Equal(t, &dbv1.GatewayTLS{Mode: "SelfSigned"}, db.Spec.TLS.Gateway)
	// The operator's default anti-affinity applies.
	assert.Nil(t, db.Spec.Affinity.EnablePodAntiAffinity)
	assert.Nil(t, db.Spec.Monitoring)
	assert.Nil(t, db.Spec.Backup)
	assert.Nil(t, db.Spec.Bootstrap)
}

func TestBuildDocumentDBFull(t *testing.T) {
	t.Parallel()
	monitoring := &dbv1.MonitoringSpec{Enabled: true}
	db := render(t, "full.yaml", monitoring)

	assert.Equal(t, "my-credentials", db.Spec.DocumentDbCredentialSecret)
	assert.Equal(t, "LoadBalancer", db.Spec.ExposeViaService.ServiceType)
	assert.Equal(t, map[string]string{"disk": "ssd"}, db.Spec.Affinity.NodeSelector)
	assert.Equal(t, &dbv1.GatewayTLS{
		Mode: "CertManager",
		CertManager: &dbv1.CertManagerTLS{
			IssuerRef: dbv1.IssuerRef{Name: "my-issuer", Kind: "ClusterIssuer", Group: "cert-manager.io"},
		},
	}, db.Spec.TLS.Gateway)
	require.NotNil(t, db.Spec.Backup)
	assert.Equal(t, 7, db.Spec.Backup.RetentionDays)
	require.NotNil(t, db.Spec.Bootstrap)
	assert.Equal(t, "my-backup", db.Spec.Bootstrap.Recovery.Backup.Name)
	assert.Same(t, monitoring, db.Spec.Monitoring)
}

func TestBuildGatewayTLS(t *testing.T) {
	t.Parallel()

	provided := buildGatewayTLS(&definition.TLSParameters{CertificateSecretName: "cert"})
	assert.Equal(t, &dbv1.GatewayTLS{Mode: "Provided", Provided: &dbv1.ProvidedTLS{SecretName: "cert"}}, provided)

	// The issuer kind defaults to a namespaced Issuer.
	issued := buildGatewayTLS(&definition.TLSParameters{Issuer: &definition.IssuerReference{Name: "ca"}})
	assert.Equal(t, "Issuer", issued.CertManager.IssuerRef.Kind)

	assert.Equal(t, "SelfSigned", buildGatewayTLS(&definition.TLSParameters{}).Mode)
}

func TestBuildAffinityReplacesDefault(t *testing.T) {
	t.Parallel()
	in := validationtest.Load(t, "testdata/validate/valid.yaml")
	engine := in.Spec.Components[common.ComponentEngine]
	engine.SchedulingPolicy = validationtest.Load(t, "testdata/validate/full.yaml").
		Spec.Components[common.ComponentEngine].SchedulingPolicy
	engine.SchedulingPolicy.Affinity = &corev1.Affinity{PodAntiAffinity: &corev1.PodAntiAffinity{}}

	affinity := buildAffinity(engine)
	require.NotNil(t, affinity.EnablePodAntiAffinity)
	assert.False(t, *affinity.EnablePodAntiAffinity)
	assert.Same(t, engine.SchedulingPolicy.Affinity.PodAntiAffinity, affinity.AdditionalPodAntiAffinity)
}

func TestConnectionURIEscapesCredentials(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"mongodb://user:p%40ss%2Fword@documentdb-service-db.ns.svc:10260/"+
			"?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&replicaSet=rs0",
		connectionURI("user", "p@ss/word", "documentdb-service-db.ns.svc"))
}
