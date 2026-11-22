package provider

import (
	"context"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	commonv1alpha1 "github.com/openeverest/openeverest/v2/api/common/v1alpha1"
	corev1alpha1 "github.com/openeverest/openeverest/v2/api/core/v1alpha1"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller"
	"github.com/openeverest/openeverest/v2/provider-runtime/controller/validationtest"

	"github.com/openeverest/provider-documentdb/internal/common"
)

func credentialsContext(t *testing.T, fixture string, objs ...client.Object) *controller.Context {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, corev1alpha1.AddToScheme(scheme))
	require.NoError(t, dbv1.AddToScheme(scheme))

	in := validationtest.Load(t, "testdata/validate/"+fixture)
	in.Namespace = "ns"
	in.UID = "uid"
	in.Spec.UserSecretRef = nil
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return controller.NewContext(context.Background(), cl, in, common.ProviderName)
}

// sourceObjects is a completed backup of the instance "source", whose
// DocumentDB names its credentials Secret.
func sourceObjects() []client.Object {
	return []client.Object{
		&dbv1.Backup{
			ObjectMeta: metav1.ObjectMeta{Name: "my-backup", Namespace: "ns"},
			Spec:       dbv1.BackupSpec{Cluster: cnpgv1.LocalObjectReference{Name: "source"}},
		},
		&dbv1.DocumentDB{
			ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "ns"},
			Spec:       dbv1.DocumentDBSpec{DocumentDbCredentialSecret: "source-users"},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "source-users", Namespace: "ns"},
			Data:       map[string][]byte{"username": []byte("admin"), "password": []byte("source-password"), "other": []byte("x")},
		},
	}
}

func TestEnsureCredentialsGeneratesOnce(t *testing.T) {
	t.Parallel()
	c := credentialsContext(t, "valid.yaml")

	name, err := ensureCredentials(c, decodeParameters(c))
	require.NoError(t, err)
	assert.Equal(t, "example-credentials", name)

	secret := &corev1.Secret{}
	require.NoError(t, c.Get(secret, name))
	assert.Equal(t, "everest", secret.StringData["username"]+string(secret.Data["username"]))
	password := secret.StringData["password"] + string(secret.Data["password"])
	assert.Len(t, password, generatedPasswordLength)

	// The gateway reads the password only once: it must never change.
	_, err = ensureCredentials(c, decodeParameters(c))
	require.NoError(t, err)
	again := &corev1.Secret{}
	require.NoError(t, c.Get(again, name))
	assert.Equal(t, password, again.StringData["password"]+string(again.Data["password"]))
}

func TestEnsureCredentialsCopiesSourceOnRestore(t *testing.T) {
	t.Parallel()
	c := credentialsContext(t, "full.yaml", sourceObjects()...)

	name, err := ensureCredentials(c, decodeParameters(c))
	require.NoError(t, err)

	secret := &corev1.Secret{}
	require.NoError(t, c.Get(secret, name))
	assert.Equal(t, map[string][]byte{"username": []byte("admin"), "password": []byte("source-password")}, secret.Data)
}

func TestEnsureCredentialsWaitsWithoutSource(t *testing.T) {
	t.Parallel()
	// The backup outlived its instance.
	c := credentialsContext(t, "full.yaml", sourceObjects()[0])

	_, err := ensureCredentials(c, decodeParameters(c))
	require.Error(t, err)
	assert.True(t, controller.IsWaitError(err))
	exists, err := c.Exists(&corev1.Secret{}, "example-credentials")
	require.NoError(t, err)
	assert.False(t, exists, "no credentials that cannot open the restored data")
}

func TestValidateReferences(t *testing.T) {
	t.Parallel()
	p := New()
	fields := func(c *controller.Context) []string {
		errs, err := p.ValidateReferences(c)
		require.NoError(t, err)
		var got []string
		for _, e := range errs {
			got = append(got, e.Field)
		}
		return got
	}
	completed := sourceObjects()
	completed[0].(*dbv1.Backup).Status.Phase = cnpgv1.BackupPhaseCompleted

	// A restore needs a completed backup and the credentials of its source.
	assert.Empty(t, fields(credentialsContext(t, "full.yaml", completed...)))
	assert.Equal(t, []string{"spec.parameters.bootstrap.backup.name"}, fields(credentialsContext(t, "full.yaml")))
	assert.Equal(t, []string{"spec.parameters.bootstrap.backup.name"},
		fields(credentialsContext(t, "full.yaml", sourceObjects()...)), "the backup has not completed")
	assert.Equal(t, []string{"spec.userSecretRef"},
		fields(credentialsContext(t, "full.yaml", completed[0])), "the source instance is gone")

	// Once the instance runs, its credentials Secret is fixed.
	running := &dbv1.DocumentDB{
		ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "ns"},
		Spec:       dbv1.DocumentDBSpec{DocumentDbCredentialSecret: "example-credentials"},
	}
	assert.Empty(t, fields(credentialsContext(t, "full.yaml", running)))
	c := credentialsContext(t, "full.yaml", running)
	c.Instance().Spec.UserSecretRef = &commonv1alpha1.SecretRef{Name: "another"}
	assert.Equal(t, []string{"spec.userSecretRef"}, fields(c))
}
