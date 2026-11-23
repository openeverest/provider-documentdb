package provider

import (
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-documentdb/definition"
)

// syncScheduledBackup keeps one ScheduledBackup per Instance while a schedule
// is set and removes it otherwise. The operator takes the backups as
// VolumeSnapshots and expires them; deleting the schedule keeps them.
func syncScheduledBackup(c *controller.Context, backup *definition.BackupParameters) error {
	if backup == nil || backup.Schedule == "" {
		existing := &dbv1.ScheduledBackup{}
		if err := c.Get(existing, c.Name()); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("get ScheduledBackup: %w", err)
		}
		if err := c.Delete(existing); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete ScheduledBackup: %w", err)
		}
		return nil
	}

	scheduled := &dbv1.ScheduledBackup{
		ObjectMeta: c.ObjectMeta(c.Name()),
		Spec: dbv1.ScheduledBackupSpec{
			Cluster:  cnpgv1.LocalObjectReference{Name: c.Name()},
			Schedule: backup.Schedule,
		},
	}
	if backup.RetentionDays != nil {
		days := int(*backup.RetentionDays)
		scheduled.Spec.RetentionDays = &days
	}
	if err := c.Apply(scheduled); err != nil {
		return fmt.Errorf("apply ScheduledBackup: %w", err)
	}
	return nil
}
