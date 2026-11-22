package provider

import (
	"crypto/rand"
	"fmt"
	"math/big"

	dbv1 "github.com/documentdb/documentdb-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/openeverest/openeverest/v2/provider-runtime/controller"

	"github.com/openeverest/provider-documentdb/definition"
)

const (
	// Keys the gateway reads from the credentials Secret.
	credentialsKeyUsername = "username"
	credentialsKeyPassword = "password"

	// generatedUsername is the administrator of generated credentials.
	// "documentdb" is taken by a role the operator creates itself.
	generatedUsername = "everest"

	generatedPasswordLength = 24
	passwordAlphabet        = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// credentialsSecretName is the Secret the gateway reads the administrator
// from: the one the user supplies, or one generated for the Instance.
func credentialsSecretName(c *controller.Context) string {
	if ref := c.Instance().Spec.UserSecretRef; ref != nil && ref.Name != "" {
		return ref.Name
	}
	return c.Name() + "-credentials"
}

// ensureCredentials returns the credentials Secret name, creating the Secret
// once when the user supplied none: with the credentials of the instance a
// backup was taken from when the instance is restored from one, generated
// otherwise. The gateway creates the user only when an instance is
// initialized, so the Secret is never rewritten.
func ensureCredentials(c *controller.Context, params definition.Parameters) (string, error) {
	name := credentialsSecretName(c)
	if ref := c.Instance().Spec.UserSecretRef; ref != nil && ref.Name != "" {
		return name, nil
	}

	err := c.Get(&corev1.Secret{}, name)
	if err == nil {
		return name, nil
	}
	if !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("get credentials secret %q: %w", name, err)
	}

	secret := &corev1.Secret{
		ObjectMeta: c.ObjectMeta(name),
		Type:       corev1.SecretTypeOpaque,
	}
	if params.Bootstrap != nil && params.Bootstrap.Backup != nil {
		// A restore brings the users of the source back with the data: the
		// restored instance only opens with their credentials.
		source, err := sourceCredentials(c, params.Bootstrap.Backup.Name)
		if err != nil {
			return "", err
		}
		if source == nil {
			// ValidateReferences reports it; without credentials that open
			// the data there is nothing to create.
			return "", controller.WaitFor("the credentials of the instance the backup was taken from are not available")
		}
		secret.Data = source
	} else {
		password, err := generatePassword(generatedPasswordLength)
		if err != nil {
			return "", fmt.Errorf("generate password: %w", err)
		}
		secret.StringData = map[string]string{
			credentialsKeyUsername: generatedUsername,
			credentialsKeyPassword: password,
		}
	}
	if err := c.Apply(secret); err != nil {
		return "", fmt.Errorf("create credentials secret %q: %w", name, err)
	}
	return name, nil
}

// sourceCredentials returns the credentials of the instance the named backup
// was taken from, read from the Secret its DocumentDB names. It returns nil
// when the backup, that DocumentDB or its Secret is gone.
func sourceCredentials(c *controller.Context, backupName string) (map[string][]byte, error) {
	backup := &dbv1.Backup{}
	if err := c.Get(backup, backupName); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get Backup %q: %w", backupName, err)
	}
	source := &dbv1.DocumentDB{}
	if err := c.Get(source, backup.Spec.Cluster.Name); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get DocumentDB %q: %w", backup.Spec.Cluster.Name, err)
	}
	secret := &corev1.Secret{}
	if err := c.Get(secret, source.Spec.DocumentDbCredentialSecret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get credentials secret %q: %w", source.Spec.DocumentDbCredentialSecret, err)
	}
	if len(secret.Data[credentialsKeyUsername]) == 0 || len(secret.Data[credentialsKeyPassword]) == 0 {
		return nil, nil
	}
	return map[string][]byte{
		credentialsKeyUsername: secret.Data[credentialsKeyUsername],
		credentialsKeyPassword: secret.Data[credentialsKeyPassword],
	}, nil
}

func generatePassword(length int) (string, error) {
	password := make([]byte, length)
	limit := big.NewInt(int64(len(passwordAlphabet)))
	for i := range password {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		password[i] = passwordAlphabet[n.Int64()]
	}
	return string(password), nil
}
