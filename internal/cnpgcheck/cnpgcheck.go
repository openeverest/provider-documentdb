// Package cnpgcheck checks the CloudNativePG installation the DocumentDB
// operator runs on, which may be the one the chart bundles or one installed
// separately, for example by provider-cloudnative-pg, and reports the result
// as a condition of the Provider.
package cnpgcheck

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
)

// ConditionType is the Provider condition the check reports.
const ConditionType = "CloudNativePGCompatible"

// SupportedMinors are the CloudNativePG releases the DocumentDB operator is
// run on: the one its chart bundles and the one provider-cloudnative-pg
// bundles. The coexist e2e suite covers both.
var SupportedMinors = []string{"1.29", "1.30"}

const (
	// operatorImage is the repository of the CloudNativePG operator image,
	// whatever registry it is pulled from.
	operatorImage = "cloudnative-pg/cloudnative-pg"
	// operatorLabel selects the operator's Deployment, as both its chart and
	// its release manifests label it.
	operatorLabel = "app.kubernetes.io/name"
	operatorName  = "cloudnative-pg"
	// pluginNameLabel marks the Services of CNPG-I plugins. CloudNativePG
	// looks for them in its own namespace only.
	pluginNameLabel = "cnpg.io/pluginName"
	// sidecarPlugin injects the DocumentDB gateway into the PostgreSQL pods.
	sidecarPlugin = "cnpg-i-sidecar-injector.documentdb.io"
)

// Reasons of the condition.
const (
	ReasonSupported          = "Supported"
	ReasonUnsupportedVersion = "UnsupportedVersion"
	ReasonVersionUnknown     = "VersionUnknown"
	ReasonNotFound           = "NotFound"
	ReasonMultipleOperators  = "MultipleOperators"
	ReasonPluginMissing      = "PluginMissing"
	ReasonCheckFailed        = "CheckFailed"
)

// Check finds the running CloudNativePG operator and reports whether the
// DocumentDB operator supports it: its version, and whether the DocumentDB
// sidecar plugin is where CloudNativePG looks for plugins.
func Check(ctx context.Context, r client.Reader) (metav1.Condition, error) {
	deployments := &appsv1.DeploymentList{}
	if err := r.List(ctx, deployments, client.MatchingLabels{operatorLabel: operatorName}); err != nil {
		return metav1.Condition{}, fmt.Errorf("list CloudNativePG deployments: %w", err)
	}
	var namespaces, versions []string
	for _, d := range deployments.Items {
		for _, container := range d.Spec.Template.Spec.Containers {
			version, ok := imageVersion(container.Image)
			if !ok || slices.Contains(namespaces, d.Namespace) {
				continue
			}
			namespaces = append(namespaces, d.Namespace)
			versions = append(versions, version)
		}
	}

	switch len(namespaces) {
	case 0:
		return condition(metav1.ConditionFalse, ReasonNotFound,
			"No CloudNativePG operator found. Enable operator.cloudnative-pg in the chart, or install CloudNativePG."), nil
	case 1:
	default:
		return condition(metav1.ConditionFalse, ReasonMultipleOperators, fmt.Sprintf(
			"CloudNativePG operators run in namespaces %s. Keep one.", strings.Join(namespaces, ", "))), nil
	}
	namespace, version := namespaces[0], versions[0]
	running := fmt.Sprintf("CloudNativePG %s in namespace %s", version, namespace)
	if version == "" {
		running = "CloudNativePG in namespace " + namespace
	}

	plugins := &corev1.ServiceList{}
	if err := r.List(ctx, plugins, client.InNamespace(namespace), client.MatchingLabels{pluginNameLabel: sidecarPlugin}); err != nil {
		return metav1.Condition{}, fmt.Errorf("list CNPG-I plugins in %s: %w", namespace, err)
	}
	if len(plugins.Items) == 0 {
		return condition(metav1.ConditionFalse, ReasonPluginMissing, fmt.Sprintf(
			"%s: the DocumentDB sidecar plugin is not in that namespace, where CloudNativePG looks for plugins. Set operator.cloudnative-pg.namespaceOverride=%s.",
			running, namespace)), nil
	}

	if version == "" {
		return condition(metav1.ConditionUnknown, ReasonVersionUnknown, running+": its image has no version tag."), nil
	}
	if !slices.Contains(SupportedMinors, minor(version)) {
		return condition(metav1.ConditionFalse, ReasonUnsupportedVersion, fmt.Sprintf(
			"%s: the DocumentDB operator supports CloudNativePG %s.", running, strings.Join(SupportedMinors, ", "))), nil
	}
	return condition(metav1.ConditionTrue, ReasonSupported, running+"."), nil
}

func condition(status metav1.ConditionStatus, reason, message string) metav1.Condition {
	return metav1.Condition{Type: ConditionType, Status: status, Reason: reason, Message: message}
}

// imageVersion returns the version tag of a CloudNativePG operator image, ""
// for a digest-only reference, and false for any other image.
func imageVersion(image string) (string, bool) {
	image, _, _ = strings.Cut(image, "@")
	repo, tag := image, ""
	// A registry port sits before the first slash, a tag after the last one.
	if i := strings.LastIndex(image, ":"); i >= 0 && !strings.Contains(image[i:], "/") {
		repo, tag = image[:i], image[i+1:]
	}
	if repo != operatorImage && !strings.HasSuffix(repo, "/"+operatorImage) {
		return "", false
	}
	return strings.TrimPrefix(tag, "v"), true
}

// minor returns the major.minor part of a version.
func minor(version string) string {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return version
	}
	return parts[0] + "." + parts[1]
}

// Reporter runs Check periodically and applies the result to the Provider's
// status.
type Reporter struct {
	// Reader reads without a cache, so the check watches nothing.
	Reader client.Reader
	Client client.Client
	// Provider is the name of the Provider resource.
	Provider string
	Interval time.Duration
	Log      logr.Logger

	last *metav1.Condition
}

// Start implements manager.Runnable. Being a plain Runnable, it runs on the
// leader only.
func (r *Reporter) Start(ctx context.Context) error {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		r.report(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Reporter) report(ctx context.Context) {
	cond, err := Check(ctx, r.Reader)
	if err != nil {
		cond = condition(metav1.ConditionUnknown, ReasonCheckFailed, err.Error())
	}
	if r.last == nil || r.last.Status != cond.Status || r.last.Message != cond.Message {
		if cond.Status == metav1.ConditionTrue {
			r.Log.Info("CloudNativePG check", "reason", cond.Reason, "message", cond.Message)
		} else {
			r.Log.Error(nil, "CloudNativePG check", "reason", cond.Reason, "message", cond.Message)
		}
	}
	r.last = &cond
	if err := r.apply(ctx, cond); err != nil {
		r.Log.Error(err, "report the CloudNativePG check on the Provider")
	}
}

// apply server-side-applies cond to the Provider's status, keeping its
// transition time while its status stays the same.
func (r *Reporter) apply(ctx context.Context, cond metav1.Condition) error {
	provider := &corev1alpha1.Provider{}
	if err := r.Reader.Get(ctx, client.ObjectKey{Name: r.Provider}, provider); err != nil {
		if apierrors.IsNotFound(err) {
			// The chart creates it; the next run reports.
			return nil
		}
		return fmt.Errorf("get Provider %s: %w", r.Provider, err)
	}
	cond.LastTransitionTime = metav1.Now()
	if current := meta.FindStatusCondition(provider.Status.Conditions, ConditionType); current != nil {
		if current.Status == cond.Status {
			cond.LastTransitionTime = current.LastTransitionTime
			if current.Reason == cond.Reason && current.Message == cond.Message {
				return nil
			}
		}
	}
	value, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&cond)
	if err != nil {
		return fmt.Errorf("convert the condition: %w", err)
	}
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": corev1alpha1.GroupVersion.String(),
		"kind":       "Provider",
		"metadata":   map[string]any{"name": r.Provider},
		"status":     map[string]any{"conditions": []any{value}},
	}}
	return r.Client.Status().Apply(ctx, client.ApplyConfigurationFromUnstructured(u),
		client.FieldOwner(controller.StatusFieldOwner(r.Provider)), client.ForceOwnership)
}
