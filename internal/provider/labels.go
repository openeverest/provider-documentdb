package provider

import (
	"fmt"
	"maps"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-documentdb/internal/common"
)

// labelPods gets the OpenEverest pod labels onto the DocumentDB pods, which
// the runtime counts into status.components and monitoring classes select.
//
// The DocumentDB resource has no pod labels field. CloudNativePG copies the
// labels of its Cluster that match INHERITED_LABELS onto the pods, which the
// chart sets to core.openeverest.io/*, so the labels go on the Cluster the
// operator creates. The operator patches the Cluster's spec only and leaves
// them alone.
func labelPods(c *controller.Context) error {
	labels := c.PodLabels(common.ComponentEngine)

	cluster := &cnpgv1.Cluster{}
	if err := c.Get(cluster, c.Name()); err != nil {
		if apierrors.IsNotFound(err) {
			// The Cluster's creation triggers the next reconcile.
			return nil
		}
		return fmt.Errorf("get CloudNativePG Cluster: %w", err)
	}

	missing := false
	for key, value := range labels {
		if cluster.Labels[key] != value {
			missing = true
			break
		}
	}
	if !missing {
		return nil
	}

	patched := cluster.DeepCopy()
	if patched.Labels == nil {
		patched.Labels = map[string]string{}
	}
	maps.Copy(patched.Labels, labels)
	if err := c.Client().Patch(c.Context(), patched, client.MergeFrom(cluster)); err != nil {
		return fmt.Errorf("label CloudNativePG Cluster: %w", err)
	}
	return nil
}
