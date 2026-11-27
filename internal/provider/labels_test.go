package provider

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openeverest/provider-documentdb/internal/common"
)

func TestLabelPodsInheritsTheLabelsThroughTheCluster(t *testing.T) {
	// The operator's own inherited labels stay.
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "ns"},
		Spec: cnpgv1.ClusterSpec{InheritedMetadata: &cnpgv1.EmbeddedObjectMetadata{
			Labels: map[string]string{"app": "example", "replica_type": "primary"},
		}},
	}
	c := postgresContext(t, nil, cluster)

	require.NoError(t, labelPods(c))

	got := &cnpgv1.Cluster{}
	require.NoError(t, c.Get(got, "example"))
	want := map[string]string{"app": "example", "replica_type": "primary"}
	for key, value := range c.PodLabels(common.ComponentEngine) {
		want[key] = value
	}
	assert.Equal(t, want, got.Spec.InheritedMetadata.Labels)
	assert.Contains(t, want, "core.openeverest.io/instance")

	// Labelled already: nothing to patch.
	require.NoError(t, labelPods(c))
	again := &cnpgv1.Cluster{}
	require.NoError(t, c.Get(again, "example"))
	assert.Equal(t, got.ResourceVersion, again.ResourceVersion)
}

func TestLabelPodsWithoutInheritedMetadata(t *testing.T) {
	cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "ns"}}
	c := postgresContext(t, nil, cluster)

	require.NoError(t, labelPods(c))

	got := &cnpgv1.Cluster{}
	require.NoError(t, c.Get(got, "example"))
	require.NotNil(t, got.Spec.InheritedMetadata)
	assert.Equal(t, c.PodLabels(common.ComponentEngine), got.Spec.InheritedMetadata.Labels)
}

func TestLabelPodsBeforeTheClusterExists(t *testing.T) {
	assert.NoError(t, labelPods(postgresContext(t, nil)))
}
