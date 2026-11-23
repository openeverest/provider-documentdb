// Package user contains the schema-bearing Go types for the "user" secret
// type. Each struct here is converted to an OpenAPI v3 schema by
// `provider-sdk generate` and inlined into the generated provider spec.
//
// +k8s:openapi-gen=true
package user

// UserSecretData describes the credentials of the administrator the gateway
// creates when the instance is initialized.
type UserSecretData struct {
	// Username of the administrator.
	// +kubebuilder:validation:MinLength=1
	Username string `json:"username"`
	// Password of the administrator.
	// +kubebuilder:validation:MinLength=8
	Password string `json:"password"`
}
