// Package common defines shared constants used across the provider.
package common

const (
	// ProviderName is this provider's identity: the name of the Provider CR it
	// ships and the value Instances put in spec.providerRef.name. It must match
	// `name` in definition/provider.yaml — the runtime uses it to fetch the
	// Provider CR, so a mismatch means nothing ever reconciles.
	ProviderName = "documentdb"

	// ComponentEngine is the DocumentDB pods: PostgreSQL with the DocumentDB
	// extension, and the gateway.
	ComponentEngine = "engine"

	// ComponentTypeDocumentDB is the type of the engine component.
	ComponentTypeDocumentDB = "documentdb"
)
