package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller/validationtest"

	"github.com/openeverest/provider-documentdb/internal/common"
)

func serviceContext(t *testing.T, want corev1.ServiceType, objs ...client.Object) *controller.Context {
	t.Helper()
	in := validationtest.Load(t, "testdata/validate/valid.yaml")
	in.Namespace = "ns"
	engine := in.Spec.Components[common.ComponentEngine]
	engine.Service = &corev1alpha1.Service{ServiceType: want}
	in.Spec.Components[common.ComponentEngine] = engine
	cl := fake.NewClientBuilder().WithObjects(objs...).Build()
	return controller.NewContext(context.Background(), cl, in, common.ProviderName)
}

func gatewayService(typ corev1.ServiceType, controller string) *corev1.Service {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "documentdb-service-example", Namespace: "ns"},
		Spec:       corev1.ServiceSpec{Type: typ},
	}
	if controller != "" {
		service.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "documentdb.io/v1", Kind: "DocumentDB", Name: controller, UID: "db", Controller: ptr.To(true),
		}}
	}
	return service
}

func storedServiceType(t *testing.T, c *controller.Context) corev1.ServiceType {
	t.Helper()
	service := &corev1.Service{}
	require.NoError(t, c.Get(service, "documentdb-service-example"))
	return service.Spec.Type
}

func TestSyncServiceType(t *testing.T) {
	t.Parallel()

	// The operator has not created the Service yet.
	require.NoError(t, syncServiceType(serviceContext(t, corev1.ServiceTypeLoadBalancer)))

	for _, tt := range []struct{ from, to corev1.ServiceType }{
		{corev1.ServiceTypeClusterIP, corev1.ServiceTypeLoadBalancer},
		{corev1.ServiceTypeLoadBalancer, corev1.ServiceTypeClusterIP},
	} {
		c := serviceContext(t, tt.to, gatewayService(tt.from, "example"))
		require.NoError(t, syncServiceType(c))
		assert.Equal(t, tt.to, storedServiceType(t, c))
	}

	// A Service of the same name the DocumentDB does not control is left alone.
	c := serviceContext(t, corev1.ServiceTypeLoadBalancer, gatewayService(corev1.ServiceTypeClusterIP, ""))
	require.NoError(t, syncServiceType(c))
	assert.Equal(t, corev1.ServiceTypeClusterIP, storedServiceType(t, c))
}
