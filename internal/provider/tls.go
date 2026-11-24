package provider

import (
	"cmp"
	"fmt"
	"net"
	"slices"
	"strings"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-documentdb/definition"
)

const (
	tlsModeSelfSigned  = "SelfSigned"
	tlsModeCertManager = "CertManager"
	tlsModeProvided    = "Provided"
)

var (
	tlsPath = field.NewPath("spec", "parameters", "tls")

	certificateGVK = schema.GroupVersionKind{Group: definition.CertManagerGroup, Version: "v1", Kind: "Certificate"}

	certManagerIssuerKinds = []definition.IssuerKind{definition.IssuerKindIssuer, definition.IssuerKindClusterIssuer}
)

// issuerOf returns the issuer tls names with its defaults, nil when it names
// none: the UI submits the default kind of an issuer it has no name for.
func issuerOf(tls *definition.TLSParameters) *definition.IssuerReference {
	if tls == nil || tls.Issuer == nil || tls.Issuer.Name == "" {
		return nil
	}
	return &definition.IssuerReference{
		Name:  tls.Issuer.Name,
		Kind:  cmp.Or(tls.Issuer.Kind, definition.IssuerKindIssuer),
		Group: cmp.Or(tls.Issuer.Group, definition.CertManagerGroup),
	}
}

// additionalNamesOf returns the extra DNS names of the certificate.
func additionalNamesOf(tls *definition.TLSParameters) []string {
	if tls == nil {
		return nil
	}
	return tls.AdditionalNames
}

// buildGatewayTLS chooses where the gateway certificate comes from. The
// gateway only accepts TLS connections whichever mode is chosen.
func buildGatewayTLS(tls *definition.TLSParameters, serviceNames []string) *dbv1.GatewayTLS {
	if tls != nil && tls.CertificateSecretName != "" {
		return &dbv1.GatewayTLS{
			Mode:     tlsModeProvided,
			Provided: &dbv1.ProvidedTLS{SecretName: tls.CertificateSecretName},
		}
	}
	issuer := issuerOf(tls)
	if issuer == nil {
		// The operator's self-signed certificate covers the Service names
		// only; syncCertificateNames adds the additional ones.
		return &dbv1.GatewayTLS{Mode: tlsModeSelfSigned}
	}
	certManager := &dbv1.CertManagerTLS{
		IssuerRef: dbv1.IssuerRef{Name: issuer.Name, Kind: string(issuer.Kind), Group: issuer.Group},
	}
	if names := additionalNamesOf(tls); len(names) > 0 {
		// dnsNames is meant to replace the names the operator chooses, so
		// the Service names are sent along.
		certManager.DNSNames = certificateDNSNames(serviceNames, names)
	}
	return &dbv1.GatewayTLS{Mode: tlsModeCertManager, CertManager: certManager}
}

// gatewayDNSNames are the names of the gateway Service, in the forms the
// operator's certificates cover.
func gatewayDNSNames(instance, namespace string) []string {
	service := gatewayServiceName(instance)
	return []string{service, service + "." + namespace, service + "." + namespace + ".svc"}
}

// certificateDNSNames are the Service names followed by the additional ones,
// without duplicates.
func certificateDNSNames(serviceNames, additional []string) []string {
	names := slices.Clone(serviceNames)
	for _, name := range additional {
		if name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// syncCertificateNames makes the gateway certificate the operator manages
// cover spec.parameters.tls.additionalNames. The operator sets the names only
// when it creates the Certificate, and later adds only missing Service names,
// so the provider updates them on an existing instance. cert-manager then
// reissues the certificate, which the gateway loads without a restart.
func syncCertificateNames(c *controller.Context, tls *definition.TLSParameters) error {
	if tls != nil && tls.CertificateSecretName != "" {
		return nil
	}
	certificate := &unstructured.Unstructured{}
	certificate.SetGroupVersionKind(certificateGVK)
	if err := c.Get(certificate, gatewayCertificateName(c.Name())); err != nil {
		// The operator has not created it yet, or cert-manager is missing,
		// which the DocumentDB status reports.
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("get gateway certificate: %w", err)
	}
	if !controlledByDocumentDB(certificate, c.Name()) {
		return nil
	}

	current, _, _ := unstructured.NestedStringSlice(certificate.Object, "spec", "dnsNames")
	want := certificateDNSNames(gatewayDNSNames(c.Name(), c.Namespace()), additionalNamesOf(tls))
	if !sameNames(current, want) {
		patch := client.MergeFromWithOptions(certificate.DeepCopy(), client.MergeFromWithOptimisticLock{})
		if err := unstructured.SetNestedStringSlice(certificate.Object, want, "spec", "dnsNames"); err != nil {
			return fmt.Errorf("set gateway certificate names: %w", err)
		}
		if err := c.Client().Patch(c.Context(), certificate, patch); err != nil {
			return fmt.Errorf("update gateway certificate names: %w", err)
		}
		return controller.WaitFor("Waiting for cert-manager to reissue the gateway certificate with spec.parameters.tls.additionalNames")
	}
	// The connection details publish the CA of the reissued certificate,
	// which changes with the self-signed one. A Certificate that has not
	// changed since the operator created it is the operator's to report.
	if certificate.GetGeneration() > 1 && !certificateReady(certificate) {
		return controller.WaitFor("Waiting for cert-manager to reissue the gateway certificate with spec.parameters.tls.additionalNames")
	}
	return nil
}

func controlledByDocumentDB(obj metav1.Object, name string) bool {
	ref := metav1.GetControllerOf(obj)
	return ref != nil && ref.Kind == "DocumentDB" && ref.Name == name
}

// sameNames reports whether a and b hold the same names in any order.
func sameNames(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

// certificateReady reports whether cert-manager has issued the Certificate
// as its current generation asks.
func certificateReady(certificate *unstructured.Unstructured) bool {
	conditions, _, _ := unstructured.NestedSlice(certificate.Object, "status", "conditions")
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok || condition["type"] != "Ready" {
			continue
		}
		observed, _, _ := unstructured.NestedInt64(condition, "observedGeneration")
		return condition["status"] == "True" && observed >= certificate.GetGeneration()
	}
	return false
}

// validateTLS checks the TLS parameters the operator cannot honour.
func validateTLS(tls *definition.TLSParameters) field.ErrorList {
	if tls == nil {
		return nil
	}
	var errs field.ErrorList
	if tls.CertificateSecretName != "" {
		if msgs := validation.IsDNS1123Subdomain(tls.CertificateSecretName); len(msgs) > 0 {
			errs = append(errs, field.Invalid(tlsPath.Child("certificateSecretName"), tls.CertificateSecretName, strings.Join(msgs, "; ")))
		}
	}
	if issuer := issuerOf(tls); issuer != nil {
		path := tlsPath.Child("issuer")
		if msgs := validation.IsDNS1123Subdomain(issuer.Name); len(msgs) > 0 {
			errs = append(errs, field.Invalid(path.Child("name"), issuer.Name, strings.Join(msgs, "; ")))
		}
		if issuer.Group == definition.CertManagerGroup && !slices.Contains(certManagerIssuerKinds, issuer.Kind) {
			errs = append(errs, field.NotSupported(path.Child("kind"), issuer.Kind, certManagerIssuerKinds))
		}
	}

	path := tlsPath.Child("additionalNames")
	for i, name := range tls.AdditionalNames {
		if net.ParseIP(name) != nil {
			errs = append(errs, field.Invalid(path.Index(i), name, "must be a DNS name, not an IP address"))
			continue
		}
		if msgs := validation.IsDNS1123Subdomain(strings.TrimPrefix(name, "*.")); len(msgs) > 0 {
			errs = append(errs, field.Invalid(path.Index(i), name, "must be a DNS name: "+strings.Join(msgs, "; ")))
		}
	}
	return errs
}

// validateTLSUpdate rejects a change of where the certificate comes from.
// The operator keeps the Certificate it created with the first issuer, and
// a different certificate Secret restarts every pod.
func validateTLSUpdate(tls, old *definition.TLSParameters) field.ErrorList {
	if certificateSource(tls) == certificateSource(old) {
		return nil
	}
	return field.ErrorList{field.Forbidden(tlsPath,
		"certificateSecretName and issuer can only be set when the instance is created; additionalNames can be changed")}
}

// certificateSource identifies where the gateway certificate comes from.
func certificateSource(tls *definition.TLSParameters) string {
	switch issuer := issuerOf(tls); {
	case tls != nil && tls.CertificateSecretName != "":
		return "secret/" + tls.CertificateSecretName
	case issuer != nil:
		return "issuer/" + issuer.Group + "/" + string(issuer.Kind) + "/" + issuer.Name
	}
	return "self-signed"
}
