package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller/validationtest"

	"github.com/openeverest/provider-documentdb/definition"
	"github.com/openeverest/provider-documentdb/internal/common"
)

func TestValidateTLS(t *testing.T) {
	t.Parallel()

	// An external issuer has kinds of its own.
	assert.Empty(t, validateTLS(&definition.TLSParameters{
		Issuer:          &definition.IssuerReference{Name: "pca", Kind: "AWSPCAClusterIssuer", Group: "awspca.cert-manager.io"},
		AdditionalNames: []string{"db.example.com", "*.example.com"},
	}))
	// Without a name the UI's default kind names no issuer.
	assert.Empty(t, validateTLS(&definition.TLSParameters{Issuer: &definition.IssuerReference{Kind: "Issuer"}}))

	errs := validateTLS(&definition.TLSParameters{Issuer: &definition.IssuerReference{Name: "ca", Kind: "AWSPCAClusterIssuer"}})
	require.Len(t, errs, 1)
	assert.Equal(t, "spec.parameters.tls.issuer.kind", errs[0].Field)
}

func TestValidateTLSUpdate(t *testing.T) {
	t.Parallel()
	issuer := func(name, kind, group string) *definition.TLSParameters {
		return &definition.TLSParameters{Issuer: &definition.IssuerReference{Name: name, Kind: definition.IssuerKind(kind), Group: group}}
	}

	// Defaults written out are the same issuer, and names can change.
	withNames := issuer("ca", "Issuer", "cert-manager.io")
	withNames.AdditionalNames = []string{"db.example.com"}
	assert.Empty(t, validateTLSUpdate(withNames, issuer("ca", "", "")))
	assert.Empty(t, validateTLSUpdate(&definition.TLSParameters{AdditionalNames: []string{"db.example.com"}}, nil))
	assert.Empty(t, validateTLSUpdate(issuer("", "Issuer", ""), nil))

	for name, tt := range map[string]struct{ tls, old *definition.TLSParameters }{
		"issuer added":     {issuer("ca", "", ""), nil},
		"issuer removed":   {nil, issuer("ca", "", "")},
		"kind":             {issuer("ca", "ClusterIssuer", ""), issuer("ca", "", "")},
		"group":            {issuer("ca", "Issuer", "example.com"), issuer("ca", "", "")},
		"secret":           {&definition.TLSParameters{CertificateSecretName: "b"}, &definition.TLSParameters{CertificateSecretName: "a"}},
		"secret to issuer": {issuer("ca", "", ""), &definition.TLSParameters{CertificateSecretName: "a"}},
	} {
		errs := validateTLSUpdate(tt.tls, tt.old)
		require.Len(t, errs, 1, name)
		assert.Equal(t, "spec.parameters.tls", errs[0].Field, name)
	}
}

// gatewayCertificate is the Certificate the operator creates for the
// DocumentDB "example" in namespace ns.
func gatewayCertificate(generation, observed int64, names ...string) *unstructured.Unstructured {
	certificate := &unstructured.Unstructured{}
	certificate.SetGroupVersionKind(certificateGVK)
	certificate.SetName("example-gateway-cert")
	certificate.SetNamespace("ns")
	certificate.SetGeneration(generation)
	certificate.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "documentdb.io/v1", Kind: "DocumentDB", Name: "example", UID: "db", Controller: ptr.To(true),
	}})
	_ = unstructured.SetNestedStringSlice(certificate.Object, names, "spec", "dnsNames")
	_ = unstructured.SetNestedSlice(certificate.Object, []any{map[string]any{
		"type": "Ready", "status": "True", "observedGeneration": observed,
	}}, "status", "conditions")
	return certificate
}

func certificateContext(t *testing.T, tls *definition.TLSParameters, certificate *unstructured.Unstructured) *controller.Context {
	t.Helper()
	in := validationtest.Load(t, "testdata/validate/valid.yaml")
	in.Namespace = "ns"
	raw, err := json.Marshal(definition.Parameters{TLS: tls})
	require.NoError(t, err)
	in.Spec.Parameters = &runtime.RawExtension{Raw: raw}
	// The fake client registers unstructured kinds in its scheme, so the
	// global one must not be shared with parallel tests.
	builder := fake.NewClientBuilder().WithScheme(runtime.NewScheme())
	if certificate != nil {
		builder = builder.WithObjects(certificate)
	}
	return controller.NewContext(context.Background(), builder.Build(), in, common.ProviderName)
}

func storedNames(t *testing.T, c *controller.Context) []string {
	t.Helper()
	certificate := &unstructured.Unstructured{}
	certificate.SetGroupVersionKind(certificateGVK)
	require.NoError(t, c.Get(certificate, "example-gateway-cert"))
	names, _, _ := unstructured.NestedStringSlice(certificate.Object, "spec", "dnsNames")
	return names
}

func TestSyncCertificateNames(t *testing.T) {
	t.Parallel()
	services := gatewayDNSNames("example", "ns")
	names := &definition.TLSParameters{AdditionalNames: []string{"db.example.com"}}

	// Before the operator creates the Certificate there is nothing to do.
	require.NoError(t, syncCertificateNames(certificateContext(t, names, nil), names))

	// The names are added to the self-signed certificate, which is then
	// waited for.
	c := certificateContext(t, names, gatewayCertificate(1, 1, services...))
	err := syncCertificateNames(c, names)
	assert.True(t, controller.IsWaitError(err), "%v", err)
	assert.Equal(t, append(services, "db.example.com"), storedNames(t, c))

	// Removed names are removed, in any order the operator wrote them.
	c = certificateContext(t, nil, gatewayCertificate(2, 2, "db.example.com", services[2], services[0], services[1]))
	assert.True(t, controller.IsWaitError(syncCertificateNames(c, nil)))
	assert.Equal(t, services, storedNames(t, c))

	// The names are there: wait until cert-manager has reissued it.
	stale := gatewayCertificate(2, 1, append(services, "db.example.com")...)
	assert.True(t, controller.IsWaitError(syncCertificateNames(certificateContext(t, names, stale), names)))
	issued := gatewayCertificate(2, 2, "db.example.com", services[0], services[1], services[2])
	require.NoError(t, syncCertificateNames(certificateContext(t, names, issued), names))
	// A new Certificate is the operator's to report on.
	require.NoError(t, syncCertificateNames(certificateContext(t, nil, gatewayCertificate(1, 0, services...)), nil))

	// A certificate Secret has no Certificate, and one the DocumentDB does
	// not control is left alone.
	secret := &definition.TLSParameters{CertificateSecretName: "cert"}
	require.NoError(t, syncCertificateNames(certificateContext(t, secret, gatewayCertificate(1, 1, "other")), secret))
	foreign := gatewayCertificate(1, 1, "other")
	foreign.SetOwnerReferences(nil)
	c = certificateContext(t, names, foreign)
	require.NoError(t, syncCertificateNames(c, names))
	assert.Equal(t, []string{"other"}, storedNames(t, c))
}
