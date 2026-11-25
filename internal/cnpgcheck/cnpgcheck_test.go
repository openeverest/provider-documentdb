package cnpgcheck

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func operator(namespace, image string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cnpg", Namespace: namespace,
			Labels: map[string]string{"app.kubernetes.io/name": "cloudnative-pg"},
		},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "manager", Image: image}},
		}}},
	}
}

func plugin(namespace string) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name: "sidecar-injector", Namespace: namespace,
		Labels: map[string]string{"cnpg.io/pluginName": "cnpg-i-sidecar-injector.documentdb.io"},
	}}
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name    string
		objs    []client.Object
		status  metav1.ConditionStatus
		reason  string
		message string
	}{
		{
			name:    "bundled",
			objs:    []client.Object{operator("cnpg-system", "ghcr.io/cloudnative-pg/cloudnative-pg:1.29.1"), plugin("cnpg-system")},
			status:  metav1.ConditionTrue,
			reason:  ReasonSupported,
			message: "CloudNativePG 1.29.1 in namespace cnpg-system.",
		},
		{
			name:    "provider-cloudnative-pg's, from a mirror",
			objs:    []client.Object{operator("everest-system", "mirror:5000/cloudnative-pg/cloudnative-pg:v1.30.2"), plugin("everest-system")},
			status:  metav1.ConditionTrue,
			reason:  ReasonSupported,
			message: "CloudNativePG 1.30.2 in namespace everest-system.",
		},
		{
			name:    "untested release",
			objs:    []client.Object{operator("cnpg-system", "ghcr.io/cloudnative-pg/cloudnative-pg:1.31.0"), plugin("cnpg-system")},
			status:  metav1.ConditionFalse,
			reason:  ReasonUnsupportedVersion,
			message: "CloudNativePG 1.31.0 in namespace cnpg-system: the DocumentDB operator supports CloudNativePG 1.29, 1.30.",
		},
		{
			name:    "plugin in another namespace",
			objs:    []client.Object{operator("everest-system", "ghcr.io/cloudnative-pg/cloudnative-pg:1.30.2"), plugin("cnpg-system")},
			status:  metav1.ConditionFalse,
			reason:  ReasonPluginMissing,
			message: "CloudNativePG 1.30.2 in namespace everest-system: the DocumentDB sidecar plugin is not in that namespace, where CloudNativePG looks for plugins. Set operator.cloudnative-pg.namespaceOverride=everest-system.",
		},
		{
			name:   "digest only",
			objs:   []client.Object{operator("cnpg-system", "ghcr.io/cloudnative-pg/cloudnative-pg@sha256:abc"), plugin("cnpg-system")},
			status: metav1.ConditionUnknown,
			reason: ReasonVersionUnknown,
		},
		{
			name:   "none",
			objs:   []client.Object{operator("cnpg-system", "ghcr.io/example/other:1.29.1")},
			status: metav1.ConditionFalse,
			reason: ReasonNotFound,
		},
		{
			name: "two",
			objs: []client.Object{
				operator("cnpg-system", "ghcr.io/cloudnative-pg/cloudnative-pg:1.29.1"),
				operator("everest-system", "ghcr.io/cloudnative-pg/cloudnative-pg:1.30.2"),
			},
			status: metav1.ConditionFalse,
			reason: ReasonMultipleOperators,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, appsv1.AddToScheme(scheme))
			require.NoError(t, corev1.AddToScheme(scheme))
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.objs...).Build()

			got, err := Check(context.Background(), cl)
			require.NoError(t, err)
			assert.Equal(t, ConditionType, got.Type)
			assert.Equal(t, tt.status, got.Status)
			assert.Equal(t, tt.reason, got.Reason)
			if tt.message != "" {
				assert.Equal(t, tt.message, got.Message)
			}
		})
	}
}
