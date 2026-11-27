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
// The DocumentDB resource has no pod labels field, so the labels go into
// spec.inheritedMetadata of the CloudNativePG Cluster the operator creates.
// CloudNativePG copies them onto the pods in place, without a restart, and
// whatever its INHERITED_LABELS setting, so any CloudNativePG installation
// works, such as the one provider-cloudnative-pg brings. The operator never
// patches spec.inheritedMetadata after creating the Cluster.
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

	patched := cluster.DeepCopy()
	if !inheritLabels(patched, labels) {
		return nil
	}
	if err := c.Client().Patch(c.Context(), patched, client.MergeFrom(cluster)); err != nil {
		return fmt.Errorf("label CloudNativePG Cluster: %w", err)
	}
	return nil
}

// inheritLabels adds labels to the Cluster's inherited metadata, keeping the
// ones the operator sets there. It reports whether anything changed.
func inheritLabels(cluster *cnpgv1.Cluster, labels map[string]string) bool {
	inherited := cluster.Spec.InheritedMetadata
	if inherited != nil {
		missing := false
		for key, value := range labels {
			if inherited.Labels[key] != value {
				missing = true
				break
			}
		}
		if !missing {
			return false
		}
	}
	if inherited == nil {
		inherited = &cnpgv1.EmbeddedObjectMetadata{}
		cluster.Spec.InheritedMetadata = inherited
	}
	if inherited.Labels == nil {
		inherited.Labels = map[string]string{}
	}
	maps.Copy(inherited.Labels, labels)
	return true
}
