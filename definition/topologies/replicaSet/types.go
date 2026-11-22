// Package replicaset contains parameters types for the replica set topology.
//
// +k8s:openapi-gen=true
package replicaset

// ReplicaSetTopologyParameters defines structured parameters for the replica
// set topology. Empty: the topology needs nothing beyond the Instance spec.
type ReplicaSetTopologyParameters struct{}
