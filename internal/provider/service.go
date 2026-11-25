package provider

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-documentdb/internal/common"
)

// syncServiceType changes the type of the gateway Service when the Instance's
// serviceType changes. The operator sets the type only when it creates the
// Service, and keeps the one it finds afterwards.
func syncServiceType(c *controller.Context) error {
	service := &corev1.Service{}
	if err := c.Get(service, gatewayServiceName(c.Name())); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get gateway service: %w", err)
	}
	if !controlledByDocumentDB(service, c.Name()) {
		return nil
	}
	want := serviceType(c.Instance().Spec.Components[common.ComponentEngine])
	if service.Spec.Type == want {
		return nil
	}
	// The API server drops the node ports when the type no longer uses them.
	patch := client.MergeFromWithOptions(service.DeepCopy(), client.MergeFromWithOptimisticLock{})
	service.Spec.Type = want
	if err := c.Client().Patch(c.Context(), service, patch); err != nil {
		return fmt.Errorf("change gateway service type: %w", err)
	}
	return nil
}
