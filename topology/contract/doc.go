// Package contract defines the topology-facing orchestration contracts for Sandstore.
//
// ControlPlaneOrchestrator owns namespace, metadata, placement, and write-intent
// coordination. DataPlaneOrchestrator owns chunk movement and per-node chunk RPCs.
// An external topology implementation provides concrete types that satisfy these
// interfaces, wires them into a server, and is then free to replace the
// hyperconverged Raft-backed implementations used by this repository.
package contract
