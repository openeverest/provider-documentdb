package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
)

// podEnvelope is the part of the DocumentDB resources that is rendered into
// every pod: CloudNativePG rolls the instances when it changes. Storage grows
// online and is not part of it.
type podEnvelope struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// holdDisruptiveChanges keeps the live value of every part of the DocumentDB
// whose change rolls the pods until the change is approved through
// spec.maintenance. Each held change is listed on status.pendingMaintenance
// with a token derived from the live and the desired value, so approving one
// change never authorizes a later, different one. Changes that do not
// restart pods (replicas, storage growth, backups, the Service type,
// additionalNames) apply at once. The version is gated separately.
func holdDisruptiveChanges(c *controller.Context, live, desired *dbv1.DocumentDB) error {
	if live.ResourceVersion == "" {
		// Creating: nothing runs yet.
		return nil
	}

	liveEnvelope := podEnvelope{CPU: live.Spec.Resource.CPU, Memory: live.Spec.Resource.Memory}
	desiredEnvelope := podEnvelope{CPU: desired.Spec.Resource.CPU, Memory: desired.Spec.Resource.Memory}
	if err := gateChange(c, "resources", "Change CPU and memory (rolling restart)",
		liveEnvelope, desiredEnvelope, func() {
			desired.Spec.Resource.CPU, desired.Spec.Resource.Memory = live.Spec.Resource.CPU, live.Spec.Resource.Memory
		}); err != nil {
		return err
	}

	if err := gateChange(c, "scheduling", "Change the scheduling policy (rolling restart)",
		live.Spec.Affinity, desired.Spec.Affinity, func() {
			desired.Spec.Affinity = *live.Spec.Affinity.DeepCopy()
		}); err != nil {
		return err
	}

	description := "Remove the metrics collector sidecar (rolling restart)"
	if collectorEnabled(desired) {
		description = "Add the metrics collector sidecar (rolling restart)"
	}
	return gateChange(c, "monitoring", description,
		live.Spec.Monitoring, desired.Spec.Monitoring, func() {
			desired.Spec.Monitoring = live.Spec.Monitoring.DeepCopy()
		})
}

// gateChange requests a rolling restart when live and desired differ and
// calls hold while it is not approved. The values are compared as the API
// stores them, so an unset field and an empty one are the same.
func gateChange(c *controller.Context, part, description string, live, desired any, hold func()) error {
	liveJSON, err := json.Marshal(live)
	if err != nil {
		return fmt.Errorf("render live %s: %w", part, err)
	}
	desiredJSON, err := json.Marshal(desired)
	if err != nil {
		return fmt.Errorf("render desired %s: %w", part, err)
	}
	if string(liveJSON) == string(desiredJSON) {
		return nil
	}
	if !c.RequestMaintenance(changeToken(part, liveJSON, desiredJSON), description, controller.MaintenanceRollingRestart) {
		hold()
	}
	return nil
}

// changeToken names one change of a part: stable while it is pending, and
// different for any other change of the same part.
func changeToken(part string, live, desired []byte) string {
	sum := sha256.New()
	sum.Write(live)
	sum.Write([]byte{0})
	sum.Write(desired)
	return part + "-" + hex.EncodeToString(sum.Sum(nil))[:10]
}
