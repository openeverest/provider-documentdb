package provider

import (
	"fmt"
	"net/url"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-documentdb/internal/common"
)

const (
	// gatewayPort is the port the gateway serves the MongoDB wire protocol on.
	gatewayPort = "10260"

	// gatewayServicePrefix prefixes the DocumentDB name in the name of the
	// Service the operator creates for the gateway of the primary.
	gatewayServicePrefix = "documentdb-service-"

	// phaseHealthy is the DocumentDB status once the cluster runs as asked:
	// the phase of the CloudNativePG Cluster, which the operator copies.
	phaseHealthy = cnpgv1.PhaseHealthy

	connectionType = "mongodb"

	// caCertificateKey is where the gateway certificate Secret holds the CA
	// and where the connection details publish it.
	caCertificateKey = "ca.crt"
)

// gatewayServiceName returns the name of the gateway Service of an instance.
func gatewayServiceName(instance string) string {
	return gatewayServicePrefix + instance
}

// readyInstances returns how many instances the CloudNativePG Cluster reports
// ready, 0 before it exists.
func readyInstances(c *controller.Context) (int32, error) {
	cluster := &cnpgv1.Cluster{}
	if err := c.Get(cluster, c.Name()); err != nil {
		if apierrors.IsNotFound(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("get CloudNativePG Cluster: %w", err)
	}
	return int32(cluster.Status.ReadyInstances), nil //nolint:gosec // at most 3
}

// Status reports whether the DocumentDB runs what the Instance asks for and
// how clients connect to it.
func (p *Provider) Status(c *controller.Context) (controller.Status, error) {
	engine := c.Instance().Spec.Components[common.ComponentEngine]
	desired := engineReplicas(engine)

	db := &dbv1.DocumentDB{}
	if err := c.Get(db, c.Name()); err != nil {
		if apierrors.IsNotFound(err) {
			return controller.Progressing("Waiting for the DocumentDB to be created").
				WithDesiredReplicas(common.ComponentEngine, desired), nil
		}
		return controller.Status{}, fmt.Errorf("get DocumentDB: %w", err)
	}

	progressing := func(message string) controller.Status {
		return controller.Progressing(message).WithDesiredReplicas(common.ComponentEngine, desired)
	}

	if db.Status.Status != phaseHealthy {
		if db.Status.Status == "" {
			return progressing("Waiting for the operator to start the cluster"), nil
		}
		return progressing(db.Status.Status), nil
	}
	// The operator copies the phase from the CloudNativePG Cluster, which
	// stays healthy for a moment after a spec change, so the instances are
	// counted as well.
	ready, err := readyInstances(c)
	if err != nil {
		return controller.Status{}, err
	}
	if ready != desired {
		return progressing(fmt.Sprintf("%d of %d instances are ready", ready, desired)), nil
	}
	if db.Status.TLS == nil || !db.Status.TLS.Ready {
		message := "Waiting for the gateway certificate"
		if db.Status.TLS != nil && db.Status.TLS.Message != "" {
			message = db.Status.TLS.Message
		}
		return progressing(message), nil
	}

	details, waiting, err := connectionDetails(c, db)
	if err != nil {
		return controller.Status{}, err
	}
	if waiting != "" {
		return progressing(waiting), nil
	}
	return controller.ReadyWithConnectionDetails(details).WithDesiredReplicas(common.ComponentEngine, desired), nil
}

// connectionDetails builds what clients need to connect. It returns a message
// instead while something it needs is not there yet.
func connectionDetails(c *controller.Context, db *dbv1.DocumentDB) (controller.ConnectionDetails, string, error) {
	credentials := &corev1.Secret{}
	if err := c.Get(credentials, db.Spec.DocumentDbCredentialSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return controller.ConnectionDetails{}, "Waiting for the credentials secret", nil
		}
		return controller.ConnectionDetails{}, "", fmt.Errorf("get credentials secret: %w", err)
	}
	username := string(credentials.Data[credentialsKeyUsername])
	password := string(credentials.Data[credentialsKeyPassword])

	// The certificate covers the Service's names without the cluster domain.
	host := fmt.Sprintf("%s.%s.svc", gatewayServiceName(c.Name()), c.Namespace())
	if db.Spec.ExposeViaService.ServiceType == string(corev1.ServiceTypeLoadBalancer) {
		service := &corev1.Service{}
		if err := c.Get(service, gatewayServiceName(c.Name())); err != nil {
			if apierrors.IsNotFound(err) {
				return controller.ConnectionDetails{}, "Waiting for the gateway service", nil
			}
			return controller.ConnectionDetails{}, "", fmt.Errorf("get gateway service: %w", err)
		}
		host = loadBalancerHost(service)
		if host == "" {
			return controller.ConnectionDetails{}, "Waiting for the load balancer address", nil
		}
	}

	details := controller.ConnectionDetails{
		Type:     connectionType,
		Host:     host,
		Port:     gatewayPort,
		Username: username,
		Password: password,
		URI:      connectionURI(username, password, host),
	}

	// Publish the CA with the connection details, so clients can verify the
	// certificate without reading the operator's Secret.
	if db.Status.TLS.SecretName != "" {
		certificate := &corev1.Secret{}
		if err := c.Get(certificate, db.Status.TLS.SecretName); err != nil && !apierrors.IsNotFound(err) {
			return controller.ConnectionDetails{}, "", fmt.Errorf("get gateway certificate secret: %w", err)
		}
		if ca := certificate.Data[caCertificateKey]; len(ca) > 0 {
			details.AdditionalProperties = map[string]string{caCertificateKey: string(ca)}
		}
	}
	return details, "", nil
}

// connectionURI builds the connection string the way the operator does. The
// gateway accepts TLS connections only and presents itself as a replica set
// of one member, hence directConnection.
func connectionURI(username, password, host string) string {
	uri := url.URL{
		Scheme:   "mongodb",
		User:     url.UserPassword(username, password),
		Host:     host + ":" + gatewayPort,
		Path:     "/",
		RawQuery: "directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&replicaSet=rs0",
	}
	return uri.String()
}

func loadBalancerHost(service *corev1.Service) string {
	for _, ingress := range service.Status.LoadBalancer.Ingress {
		if ingress.Hostname != "" {
			return ingress.Hostname
		}
		if ingress.IP != "" {
			return ingress.IP
		}
	}
	return ""
}
