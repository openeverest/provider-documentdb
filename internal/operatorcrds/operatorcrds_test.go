package operatorcrds

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var operatorCRDNames = []string{
	"backups.documentdb.io",
	"dbs.documentdb.io",
	"scheduledbackups.documentdb.io",
}

func TestCRDs(t *testing.T) {
	t.Parallel()

	crds, err := CRDs()

	require.NoError(t, err)
	names := make([]string, 0, len(crds))
	for _, crd := range crds {
		names = append(names, crd.GetName())
		assert.NotContains(t, crd.Object, "status", "a server-side apply must not carry status")
	}
	assert.ElementsMatch(t, operatorCRDNames, names)
}

func TestApply(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	require.NoError(t, apiextensionsv1.AddToScheme(scheme))
	stale := &apiextensionsv1.CustomResourceDefinition{}
	stale.Name = "dbs.documentdb.io"
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale).Build()

	require.NoError(t, Apply(context.Background(), c))

	for _, name := range operatorCRDNames {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name}, crd), name)
		assert.Equal(t, "documentdb.io", crd.Spec.Group, name)
		assert.NotEmpty(t, crd.Spec.Versions, name)
	}
}
