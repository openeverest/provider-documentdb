// Package provider implements the OpenEverest provider for DocumentDB on top
// of the DocumentDB Kubernetes Operator.
package provider

import (
	"context"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	monitoringv1alpha1 "github.com/openeverest/openeverest/v2/api/monitoring/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-documentdb/internal/common"
)

var (
	_ controller.ProviderInterface         = (*Provider)(nil)
	_ controller.MonitoringSourcesProvider = (*Provider)(nil)
)

// Provider implements controller.ProviderInterface for DocumentDB.
type Provider struct {
	controller.BaseProvider
}

// New creates a new Provider instance.
func New() *Provider {
	return &Provider{
		BaseProvider: controller.BaseProvider{
			ProviderName: common.ProviderName,
			SchemeFuncs: []func(*runtime.Scheme) error{
				dbv1.AddToScheme,
				cnpgv1.AddToScheme,
				// Sync lists MonitoringBindings.
				monitoringv1alpha1.SchemeBuilder.AddToScheme,
			},
			WatchConfigs: []controller.WatchConfig{
				controller.WatchOwned(&dbv1.DocumentDB{}),
				controller.WatchOwned(&dbv1.ScheduledBackup{}),
				// The operator owns the CloudNativePG Cluster and the gateway
				// Service, which carry what the DocumentDB status does not:
				// how many instances are ready and the load balancer address.
				controller.WatchExternal(&cnpgv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(enqueueDocumentDBOwner)),
				controller.WatchExternal(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(enqueueDocumentDBOwner)),
			},
		},
	}
}

// enqueueDocumentDBOwner maps an object the operator created for a DocumentDB
// to the Instance of the same name. The runtime drops requests for Instances
// of other providers.
func enqueueDocumentDBOwner(_ context.Context, obj client.Object) []reconcile.Request {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Kind == "DocumentDB" && ref.Controller != nil && *ref.Controller {
			return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: obj.GetNamespace(), Name: ref.Name}}}
		}
	}
	return nil
}

// Sync renders the Instance into the operator's resources: the credentials
// Secret, the DocumentDB and, when backups are scheduled, a ScheduledBackup.
// It keeps the names of the gateway certificate up to date.
func (p *Provider) Sync(c *controller.Context) error {
	params := decodeParameters(c)

	secretName, err := ensureCredentials(c, params)
	if err != nil {
		return err
	}

	monitoring, err := monitoringSpec(c)
	if err != nil {
		return err
	}

	if err := c.Apply(buildDocumentDB(c, params, secretName, monitoring)); err != nil {
		return err
	}

	if err := syncScheduledBackup(c, params.Backup); err != nil {
		return err
	}

	if err := labelPods(c); err != nil {
		return err
	}

	if err := syncServiceType(c); err != nil {
		return err
	}

	return syncCertificateNames(c, params.TLS)
}

// Cleanup deletes the DocumentDB and, once it is gone, the gateway
// certificate Secret, which cert-manager leaves behind with the private key.
// The ScheduledBackup and the generated credentials are owned by the Instance
// and garbage collected with it. Backups the operator took stay until their
// retention expires, so a deleted instance can still be restored.
func (p *Provider) Cleanup(c *controller.Context) error {
	db := &dbv1.DocumentDB{}
	err := c.Get(db, c.Name())
	if err == nil {
		if db.DeletionTimestamp.IsZero() {
			if err := c.Delete(db); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("delete DocumentDB: %w", err)
			}
		}
		// The certificate would be issued again while the DocumentDB exists.
		return controller.WaitFor("the DocumentDB is being deleted")
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get DocumentDB: %w", err)
	}

	secret := &corev1.Secret{}
	if err := c.Get(secret, gatewayCertificateName(c.Name())+"-tls"); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get gateway certificate secret: %w", err)
	}
	// Only the Secret cert-manager issued for the instance, never one the
	// user provided.
	if secret.Annotations[certManagerCertificateNameAnnotation] != gatewayCertificateName(c.Name()) {
		return nil
	}
	if err := c.Delete(secret); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete gateway certificate secret: %w", err)
	}
	return nil
}

// certManagerCertificateNameAnnotation is set by cert-manager on the Secret
// of a Certificate.
const certManagerCertificateNameAnnotation = "cert-manager.io/certificate-name"

// gatewayCertificateName returns the name of the Certificate the operator
// requests for the gateway of an instance; its Secret adds the suffix "-tls".
func gatewayCertificateName(instance string) string {
	return instance + "-gateway-cert"
}
