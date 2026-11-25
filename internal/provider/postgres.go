package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
)

// DefaultPostgresImage is the PostgreSQL image the provider runs DocumentDB
// on in ImageVolume mode. It is the default of the CloudNativePG release the
// bundled operator chart ships (1.29.1), which is what instances created
// before the provider pinned the image run, so pinning it restarts nothing.
const DefaultPostgresImage = "ghcr.io/cloudnative-pg/postgresql:18.3-system-trixie"

// imageModeAnnotation is set by the operator on the CloudNativePG Cluster.
const (
	imageModeAnnotation = "documentdb.io/image-mode"
	imageModeCombined   = "combined"
)

// postgresImage returns the PostgreSQL image to render on the DocumentDB.
//
// Left unset, the operator falls back to its POSTGRES_IMAGE setting and then
// to CloudNativePG's built-in default, so upgrading either operator would
// move every instance to a new PostgreSQL image and restart it without
// asking. The provider pins the image instead, and a change of the image a
// running instance uses waits for maintenance approval, whether it comes from
// a new provider release or from an instance created before the pin.
func postgresImage(c *controller.Context, target string) (string, error) {
	cluster := &cnpgv1.Cluster{}
	if err := c.Get(cluster, c.Name()); err != nil {
		if apierrors.IsNotFound(err) {
			// Nothing runs yet.
			return target, nil
		}
		return "", fmt.Errorf("get CloudNativePG Cluster: %w", err)
	}
	// The combined image ships PostgreSQL; the operator ignores the
	// PostgreSQL image then.
	if combinedImageMode(cluster) {
		return target, nil
	}

	running := cluster.Spec.ImageName
	if running == "" || running == target {
		return target, nil
	}
	if c.RequestMaintenance(postgresImageToken(running, target),
		fmt.Sprintf("Change the PostgreSQL image from %s to %s (rolling restart)", running, target),
		controller.MaintenanceRollingRestart) {
		return target, nil
	}
	return running, nil
}

// combinedImageMode reports whether the operator runs the cluster from the
// combined PostgreSQL and DocumentDB image. Clusters older than the
// annotation run in combined mode when they mount no extension image.
func combinedImageMode(cluster *cnpgv1.Cluster) bool {
	if mode, ok := cluster.Annotations[imageModeAnnotation]; ok {
		return mode == imageModeCombined
	}
	return len(cluster.Spec.PostgresConfiguration.Extensions) == 0
}

// postgresImageToken names one change of the image: stable while it is
// pending, and different for any other change.
func postgresImageToken(running, target string) string {
	sum := sha256.Sum256([]byte(running + "\x00" + target))
	return "postgres-image-" + hex.EncodeToString(sum[:])[:10]
}

// setPostgresImage renders the PostgreSQL image. The extension and gateway
// images stay unset: the operator derives them from the DocumentDB version.
func setPostgresImage(db *dbv1.DocumentDB, image string) {
	db.Spec.Image = &dbv1.ImageSpec{Postgres: image}
}
