package provider

import (
	"fmt"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/intstr"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-documentdb/internal/common"
)

const (
	// postgresMetricsPort is the CloudNativePG instance manager's metrics
	// port, which every pod serves.
	postgresMetricsPort = "metrics"

	// collectorMetricsPort is where the operator's OpenTelemetry Collector
	// sidecar serves the DocumentDB metrics. 9187 is taken by the instance
	// manager.
	collectorMetricsPort = 9188
	// collectorMetricsPortName is the name the operator gives that port.
	collectorMetricsPortName = "prom-metrics"

	metricsPath = "/metrics"
)

// liveDocumentDB returns the DocumentDB as the cluster holds it, empty before
// it exists.
func liveDocumentDB(c *controller.Context) (*dbv1.DocumentDB, error) {
	live := &dbv1.DocumentDB{}
	if err := c.Get(live, c.Name()); err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get live DocumentDB: %w", err)
	}
	return live, nil
}

func collectorEnabled(db *dbv1.DocumentDB) bool {
	m := db.Spec.Monitoring
	return m != nil && m.Enabled && m.Exporter != nil && m.Exporter.Prometheus != nil
}

// monitoringSpec turns the operator's OpenTelemetry Collector sidecar on
// while the metrics source policy or a bound class requires metrics. Adding
// or removing the sidecar rolls the pods, so while the requirement cannot be
// resolved the live setting is kept, and a change waits for maintenance
// approval (holdDisruptiveChanges).
func monitoringSpec(c *controller.Context, live *dbv1.DocumentDB) (*dbv1.MonitoringSpec, error) {
	req, err := c.RequiredSources()
	if err != nil {
		return nil, err
	}
	if !req.Metrics.Else(collectorEnabled(live)) {
		return nil, nil
	}
	return &dbv1.MonitoringSpec{
		Enabled: true,
		Exporter: &dbv1.ExporterSpec{
			Prometheus: &dbv1.PrometheusExporterSpec{Port: collectorMetricsPort},
		},
	}, nil
}

// MonitoringSources implements controller.MonitoringSourcesProvider. Once an
// instance is ready, every pod serves the PostgreSQL metrics of the
// CloudNativePG instance manager and, while the collector sidecar runs, the
// DocumentDB metrics.
func (p *Provider) MonitoringSources(c *controller.Context) (*corev1alpha1.MonitoringSources, error) {
	sources := &corev1alpha1.MonitoringSources{}

	live, err := liveDocumentDB(c)
	if err != nil {
		return nil, err
	}
	ready, err := readyInstances(c)
	if err != nil {
		return nil, err
	}
	if ready == 0 {
		return sources, nil
	}

	selector := map[string]string{
		controller.InstanceLabel:  c.Name(),
		controller.ComponentLabel: common.ComponentEngine,
	}
	sources.Metrics = append(sources.Metrics, corev1alpha1.MetricsEndpoint{
		Component:   common.ComponentEngine,
		PodSelector: selector,
		Port:        intstr.FromString(postgresMetricsPort),
		Path:        metricsPath,
		Scheme:      "http",
	})
	if collectorEnabled(live) {
		sources.Metrics = append(sources.Metrics, corev1alpha1.MetricsEndpoint{
			Component:   common.ComponentEngine,
			PodSelector: selector,
			Port:        intstr.FromString(collectorMetricsPortName),
			Path:        metricsPath,
			Scheme:      "http",
		})
	}
	return sources, nil
}
