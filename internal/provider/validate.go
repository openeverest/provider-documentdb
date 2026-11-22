package provider

import (
	"encoding/json"
	"fmt"
	"reflect"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/robfig/cron"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-documentdb/definition"
	"github.com/openeverest/provider-documentdb/internal/common"
)

// maxInstanceNameLength keeps the gateway Service name, the instance name
// behind gatewayServicePrefix, within the 63 characters of a DNS label.
const maxInstanceNameLength = validation.DNS1035LabelMaxLength - len(gatewayServicePrefix)

// Validate checks the rules about the Instance alone that the Provider CR
// cannot declare. The runtime has already checked the topology, components,
// versions, parameter schemas and supportedFields.
func (p *Provider) Validate(in controller.ValidationInput) controller.ValidationResult {
	var errs field.ErrorList
	spec := field.NewPath("spec")

	errs = append(errs, validateName(in.Instance.Name)...)
	errs = append(errs, validateEngine(in.Instance)...)

	// OpenEverest backups write to a BackupStorage, an object store. The
	// operator only takes VolumeSnapshots, which parameters.backup configures.
	if backup := in.Instance.Spec.Backup; backup != nil && (backup.Enabled || len(backup.Storages) > 0) {
		errs = append(errs, field.Forbidden(spec.Child("backup"), fmt.Sprintf(
			"provider %q does not back up to backup storages; schedule VolumeSnapshot backups with spec.parameters.backup",
			common.ProviderName)))
	}
	if in.Instance.Spec.DataSource != nil {
		errs = append(errs, field.Forbidden(spec.Child("dataSource"), fmt.Sprintf(
			"provider %q restores from the operator's backups; set spec.parameters.bootstrap.backup.name",
			common.ProviderName)))
	}

	params, err := parametersOf(in.Instance)
	if err != nil {
		// The runtime reports parameters that do not match the schema.
		return controller.ValidationResult{Errors: errs}
	}
	if params.Backup != nil && params.Backup.Schedule != "" {
		if _, err := cron.ParseStandard(params.Backup.Schedule); err != nil {
			errs = append(errs, field.Invalid(spec.Child("parameters", "backup", "schedule"),
				params.Backup.Schedule, "must be a cron expression of five fields, such as \"0 2 * * *\": "+err.Error()))
		}
	}

	if in.Old != nil {
		errs = append(errs, validateUpdate(in.Instance, in.Old, params)...)
	}
	return controller.ValidationResult{Errors: errs}
}

// parametersOf decodes the instance-wide parameters, empty when unset.
func parametersOf(in *corev1alpha1.Instance) (definition.Parameters, error) {
	var params definition.Parameters
	if in.Spec.Parameters == nil || len(in.Spec.Parameters.Raw) == 0 {
		return params, nil
	}
	err := json.Unmarshal(in.Spec.Parameters.Raw, &params)
	return params, err
}

func validateName(name string) field.ErrorList {
	path := field.NewPath("metadata", "name")
	var errs field.ErrorList
	for _, msg := range validation.IsDNS1035Label(name) {
		errs = append(errs, field.Invalid(path, name, msg))
	}
	if len(name) > maxInstanceNameLength {
		errs = append(errs, field.TooLong(path, name, maxInstanceNameLength))
	}
	return errs
}

// validateEngine checks the engine fields the operator restricts further
// than supportedFields can say.
func validateEngine(in *corev1alpha1.Instance) field.ErrorList {
	var errs field.ErrorList
	path := field.NewPath("spec", "components").Key(common.ComponentEngine)
	engine, ok := in.Spec.Components[common.ComponentEngine]
	if !ok {
		return nil
	}

	if engine.Storage != nil && engine.Storage.Size.Sign() <= 0 {
		errs = append(errs, field.Required(path.Child("storage", "size"), "the size of the data volume"))
	}
	if engine.Resources != nil {
		for name := range engine.Resources.Limits {
			if name != corev1.ResourceCPU && name != corev1.ResourceMemory {
				errs = append(errs, field.NotSupported(path.Child("resources", "limits"), name,
					[]corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory}))
			}
		}
	}
	return errs
}

// validateUpdate checks the transitions the operator refuses, so they are
// rejected when the Instance is written instead of failing every Sync.
func validateUpdate(in, old *corev1alpha1.Instance, params definition.Parameters) field.ErrorList {
	var errs field.ErrorList
	spec := field.NewPath("spec")

	oldParams, err := parametersOf(old)
	if err == nil && !reflect.DeepEqual(params.Bootstrap, oldParams.Bootstrap) {
		errs = append(errs, field.Forbidden(spec.Child("parameters", "bootstrap"),
			"can only be set when the instance is created"))
	}

	// Core lets an unset userSecretRef be set later, but the gateway creates
	// its user from the Secret the instance started with.
	if in.Spec.UserSecretRef != nil && old.Spec.UserSecretRef == nil {
		errs = append(errs, field.Forbidden(spec.Child("userSecretRef"),
			"can only be set when the instance is created"))
	}

	engine, oldEngine := in.Spec.Components[common.ComponentEngine], old.Spec.Components[common.ComponentEngine]
	if engine.Storage == nil || oldEngine.Storage == nil {
		return errs
	}
	storage := spec.Child("components").Key(common.ComponentEngine).Child("storage")
	if engine.Storage.Size.Cmp(oldEngine.Storage.Size) < 0 {
		errs = append(errs, field.Forbidden(storage.Child("size"), fmt.Sprintf(
			"storage can only grow; %s is less than %s", engine.Storage.Size.String(), oldEngine.Storage.Size.String())))
	}
	if !reflect.DeepEqual(engine.Storage.StorageClass, oldEngine.Storage.StorageClass) {
		errs = append(errs, field.Forbidden(storage.Child("storageClass"),
			"the storage class cannot be changed after the instance is created"))
	}
	return errs
}

// ValidateReferences checks the objects the Instance names once they exist:
// the keys of the credentials Secret, and that the backup a new instance is
// restored from has completed and its credentials are known.
func (p *Provider) ValidateReferences(c *controller.Context) (field.ErrorList, error) {
	var errs field.ErrorList
	spec := field.NewPath("spec")

	if ref := c.Instance().Spec.UserSecretRef; ref != nil && ref.Name != "" {
		secret := &corev1.Secret{}
		err := c.Get(secret, ref.Name)
		switch {
		case apierrors.IsNotFound(err):
		case err != nil:
			return nil, fmt.Errorf("get user secret %q: %w", ref.Name, err)
		default:
			for _, key := range []string{credentialsKeyUsername, credentialsKeyPassword} {
				if len(secret.Data[key]) == 0 {
					errs = append(errs, field.Invalid(spec.Child("userSecretRef", "name"), ref.Name,
						fmt.Sprintf("the secret must have a non-empty %q key", key)))
				}
			}
		}
	}

	live := &dbv1.DocumentDB{}
	if err := c.Get(live, c.Name()); err == nil {
		// The gateway created its user from the Secret the DocumentDB names
		// and the operator refuses another one. The bootstrap has been used
		// as well; its backup may expire afterwards.
		if live.Spec.DocumentDbCredentialSecret != credentialsSecretName(c) {
			errs = append(errs, field.Forbidden(spec.Child("userSecretRef"), fmt.Sprintf(
				"the credentials secret cannot be changed after the instance is created; it is %q",
				live.Spec.DocumentDbCredentialSecret)))
		}
		return errs, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get DocumentDB: %w", err)
	}

	params := decodeParameters(c)
	if params.Bootstrap == nil || params.Bootstrap.Backup == nil {
		return errs, nil
	}
	name := params.Bootstrap.Backup.Name
	backup := &dbv1.Backup{}
	err := c.Get(backup, name)
	switch {
	case apierrors.IsNotFound(err):
		return append(errs, field.NotFound(spec.Child("parameters", "bootstrap", "backup", "name"), name)), nil
	case err != nil:
		return nil, fmt.Errorf("get Backup %q: %w", name, err)
	case backup.Status.Phase != cnpgv1.BackupPhaseCompleted:
		return append(errs, field.Invalid(spec.Child("parameters", "bootstrap", "backup", "name"), name,
			fmt.Sprintf("the backup is not completed (phase %q)", backup.Status.Phase))), nil
	}

	// The restored data only opens with the credentials of its source. The
	// provider copies them while the source instance exists.
	if ref := c.Instance().Spec.UserSecretRef; ref == nil || ref.Name == "" {
		if exists, err := c.Exists(&corev1.Secret{}, credentialsSecretName(c)); err != nil {
			return nil, fmt.Errorf("get credentials secret: %w", err)
		} else if !exists {
			source, err := sourceCredentials(c, name)
			if err != nil {
				return nil, err
			}
			if source == nil {
				errs = append(errs, field.Required(spec.Child("userSecretRef"), fmt.Sprintf(
					"the instance %q the backup was taken from is gone: name a Secret with its username and password",
					backup.Spec.Cluster.Name)))
			}
		}
	}
	return errs, nil
}
