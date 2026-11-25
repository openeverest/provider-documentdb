package provider

import (
	"context"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller/validationtest"

	"github.com/openeverest/provider-documentdb/internal/common"
)

const (
	runningImage = "ghcr.io/cloudnative-pg/postgresql:18.3-system-trixie"
	targetImage  = "ghcr.io/cloudnative-pg/postgresql:18.7-system-trixie"
)

func postgresContext(t *testing.T, maintenance *corev1alpha1.MaintenanceSpec, objs ...client.Object) *controller.Context {
	t.Helper()
	in := validationtest.Load(t, "testdata/validate/valid.yaml")
	in.Namespace = "ns"
	in.Spec.Maintenance = maintenance
	scheme := runtime.NewScheme()
	require.NoError(t, cnpgv1.AddToScheme(scheme))
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return controller.NewContext(context.Background(), cl, in, common.ProviderName)
}

func cnpgCluster(mode, image string, extensions bool) *cnpgv1.Cluster {
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "ns"},
		Spec:       cnpgv1.ClusterSpec{ImageName: image},
	}
	if mode != "" {
		cluster.Annotations = map[string]string{imageModeAnnotation: mode}
	}
	if extensions {
		cluster.Spec.PostgresConfiguration.Extensions = []cnpgv1.ExtensionConfiguration{{Name: "documentdb"}}
	}
	return cluster
}

func TestPostgresImage(t *testing.T) {
	t.Parallel()
	token := postgresImageToken(runningImage, targetImage)

	for _, tt := range []struct {
		name        string
		cluster     *cnpgv1.Cluster
		maintenance *corev1alpha1.MaintenanceSpec
		want        string
		pending     bool
	}{
		{name: "new instance", want: targetImage},
		{name: "already on the target", cluster: cnpgCluster("imageVolume", targetImage, true), want: targetImage},
		{name: "change held", cluster: cnpgCluster("imageVolume", runningImage, true), want: runningImage, pending: true},
		{
			name: "change approved", cluster: cnpgCluster("imageVolume", runningImage, true),
			maintenance: &corev1alpha1.MaintenanceSpec{Approved: token}, want: targetImage,
		},
		{
			name: "approval of another change", cluster: cnpgCluster("imageVolume", runningImage, true),
			maintenance: &corev1alpha1.MaintenanceSpec{Approved: postgresImageToken(runningImage, "other")},
			want:        runningImage, pending: true,
		},
		{
			name: "rolling restarts auto-approved", cluster: cnpgCluster("imageVolume", runningImage, true),
			maintenance: &corev1alpha1.MaintenanceSpec{AutoApproveUpTo: corev1alpha1.MaintenanceRollingRestart},
			want:        targetImage,
		},
		// The operator ignores the PostgreSQL image in combined mode.
		{name: "combined mode", cluster: cnpgCluster("combined", "ghcr.io/documentdb/combined:18-1.0.1", false), want: targetImage},
		{name: "combined mode without annotation", cluster: cnpgCluster("", "ghcr.io/documentdb/combined:18-1.0.1", false), want: targetImage},
		{name: "ImageVolume mode without annotation", cluster: cnpgCluster("", runningImage, true), want: runningImage, pending: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var objs []client.Object
			if tt.cluster != nil {
				objs = append(objs, tt.cluster)
			}
			c := postgresContext(t, tt.maintenance, objs...)

			image, err := postgresImage(c, targetImage)
			require.NoError(t, err)
			assert.Equal(t, tt.want, image)

			pending := c.GetPendingMaintenance()
			if !tt.pending {
				assert.Empty(t, pending)
				return
			}
			require.Len(t, pending, 1)
			assert.Equal(t, token, pending[0].ApprovalToken)
			assert.Equal(t, corev1alpha1.MaintenanceRollingRestart, pending[0].Severity)
			assert.Contains(t, pending[0].Description, targetImage)
		})
	}
}
