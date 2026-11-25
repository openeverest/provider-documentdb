package provider

import (
	"context"
	"strings"
	"testing"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"

	apicommon "github.com/openeverest/openeverest/v2/api/common/v1alpha1"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller/validationtest"

	"github.com/openeverest/provider-documentdb/internal/common"
)

// liveFrom returns db as the API server holds it after it was applied.
func liveFrom(db *dbv1.DocumentDB) *dbv1.DocumentDB {
	live := db.DeepCopy()
	live.ResourceVersion = "1"
	return live
}

// gate renders the Instance fixture, changed by edit, against live and
// returns the DocumentDB to apply with the actions held for approval.
func gate(
	t *testing.T, live *dbv1.DocumentDB, maintenance *corev1alpha1.MaintenanceSpec, monitoring *dbv1.MonitoringSpec,
	edit func(*corev1alpha1.ComponentSpec),
) (*dbv1.DocumentDB, []corev1alpha1.PendingMaintenanceAction) {
	t.Helper()
	in := validationtest.Load(t, "testdata/validate/valid.yaml")
	in.Namespace = "ns"
	in.Spec.Maintenance = maintenance
	engine := in.Spec.Components[common.ComponentEngine]
	engine.Version = "1.0.1"
	if edit != nil {
		edit(&engine)
	}
	in.Spec.Components[common.ComponentEngine] = engine

	c := controller.NewContext(context.Background(), nil, in, common.ProviderName)
	db := buildDocumentDB(c, decodeParameters(c), credentialsSecretName(c), monitoring)
	require.NoError(t, holdDisruptiveChanges(c, live, db))
	return db, c.GetPendingMaintenance()
}

func moreResources(engine *corev1alpha1.ComponentSpec) {
	engine.Resources.Limits["cpu"] = resource.MustParse("2")
	engine.Resources.Limits["memory"] = resource.MustParse("4Gi")
}

func onSSDNodes(engine *corev1alpha1.ComponentSpec) {
	engine.SchedulingPolicy = &apicommon.SchedulingPolicy{NodeSelector: map[string]string{"disk": "ssd"}}
}

var collector = &dbv1.MonitoringSpec{
	Enabled:  true,
	Exporter: &dbv1.ExporterSpec{Prometheus: &dbv1.PrometheusExporterSpec{Port: collectorMetricsPort}},
}

func TestHoldDisruptiveChanges(t *testing.T) {
	t.Parallel()
	current, _ := gate(t, &dbv1.DocumentDB{}, nil, nil, nil)
	live := liveFrom(current)

	t.Run("a new instance is created as asked", func(t *testing.T) {
		t.Parallel()
		db, pending := gate(t, &dbv1.DocumentDB{}, nil, collector, func(e *corev1alpha1.ComponentSpec) {
			moreResources(e)
			onSSDNodes(e)
		})
		assert.Empty(t, pending)
		assert.Equal(t, "2", db.Spec.Resource.CPU)
		assert.Equal(t, map[string]string{"disk": "ssd"}, db.Spec.Affinity.NodeSelector)
		assert.Equal(t, collector, db.Spec.Monitoring)
	})

	t.Run("changes that do not restart pods apply at once", func(t *testing.T) {
		t.Parallel()
		db, pending := gate(t, live, nil, nil, func(e *corev1alpha1.ComponentSpec) {
			e.Replicas = ptr.To[int32](1)
			e.Storage.Size = resource.MustParse("20Gi")
			// An empty scheduling policy renders as none.
			e.SchedulingPolicy = &apicommon.SchedulingPolicy{NodeSelector: map[string]string{}}
		})
		assert.Empty(t, pending)
		assert.Equal(t, 1, db.Spec.InstancesPerNode)
		assert.Equal(t, "20Gi", db.Spec.Resource.Storage.PvcSize)
	})

	for _, tt := range []struct {
		name        string
		monitoring  *dbv1.MonitoringSpec
		edit        func(*corev1alpha1.ComponentSpec)
		prefix      string
		description string
		held        func(t *testing.T, db *dbv1.DocumentDB)
		applied     func(t *testing.T, db *dbv1.DocumentDB)
	}{{
		name:        "resources",
		edit:        moreResources,
		prefix:      "resources-",
		description: "CPU and memory",
		held: func(t *testing.T, db *dbv1.DocumentDB) {
			assert.Equal(t, "1", db.Spec.Resource.CPU)
			assert.Equal(t, "2Gi", db.Spec.Resource.Memory)
		},
		applied: func(t *testing.T, db *dbv1.DocumentDB) {
			assert.Equal(t, "2", db.Spec.Resource.CPU)
			assert.Equal(t, "4Gi", db.Spec.Resource.Memory)
		},
	}, {
		name:        "scheduling",
		edit:        onSSDNodes,
		prefix:      "scheduling-",
		description: "scheduling policy",
		held: func(t *testing.T, db *dbv1.DocumentDB) {
			assert.Empty(t, db.Spec.Affinity.NodeSelector)
		},
		applied: func(t *testing.T, db *dbv1.DocumentDB) {
			assert.Equal(t, map[string]string{"disk": "ssd"}, db.Spec.Affinity.NodeSelector)
		},
	}, {
		name:        "monitoring",
		monitoring:  collector,
		prefix:      "monitoring-",
		description: "Add the metrics collector",
		held: func(t *testing.T, db *dbv1.DocumentDB) {
			assert.Nil(t, db.Spec.Monitoring)
		},
		applied: func(t *testing.T, db *dbv1.DocumentDB) {
			assert.Equal(t, collector, db.Spec.Monitoring)
		},
	}} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db, pending := gate(t, live, nil, tt.monitoring, tt.edit)
			require.Len(t, pending, 1)
			action := pending[0]
			assert.True(t, strings.HasPrefix(action.ApprovalToken, tt.prefix), action.ApprovalToken)
			assert.Contains(t, action.Description, tt.description)
			assert.Equal(t, controller.MaintenanceRollingRestart, action.Severity)
			tt.held(t, db)

			// The token is stable while the change is pending.
			_, again := gate(t, live, nil, tt.monitoring, tt.edit)
			assert.Equal(t, pending, again)

			db, pending = gate(t, live, &corev1alpha1.MaintenanceSpec{Approved: action.ApprovalToken}, tt.monitoring, tt.edit)
			assert.Empty(t, pending)
			tt.applied(t, db)

			db, pending = gate(t, live, &corev1alpha1.MaintenanceSpec{AutoApproveUpTo: controller.MaintenanceRollingRestart}, tt.monitoring, tt.edit)
			assert.Empty(t, pending)
			tt.applied(t, db)

			// Once applied, nothing is pending.
			_, pending = gate(t, liveFrom(db), nil, tt.monitoring, tt.edit)
			assert.Empty(t, pending)
		})
	}

	t.Run("removing the collector", func(t *testing.T) {
		t.Parallel()
		withCollector := live.DeepCopy()
		withCollector.Spec.Monitoring = collector.DeepCopy()
		db, pending := gate(t, withCollector, nil, nil, nil)
		require.Len(t, pending, 1)
		assert.Contains(t, pending[0].Description, "Remove the metrics collector")
		assert.Equal(t, collector, db.Spec.Monitoring)
	})

	t.Run("an approval covers only its change", func(t *testing.T) {
		t.Parallel()
		_, pending := gate(t, live, nil, nil, moreResources)
		require.Len(t, pending, 1)
		approved := &corev1alpha1.MaintenanceSpec{Approved: pending[0].ApprovalToken}

		db, pending := gate(t, live, approved, collector, func(e *corev1alpha1.ComponentSpec) {
			e.Resources.Limits["cpu"] = resource.MustParse("3")
			onSSDNodes(e)
		})
		assert.Len(t, pending, 3)
		assert.Equal(t, "1", db.Spec.Resource.CPU)
		assert.NotContains(t, []string{pending[0].ApprovalToken, pending[1].ApprovalToken, pending[2].ApprovalToken}, approved.Approved)
	})
}
