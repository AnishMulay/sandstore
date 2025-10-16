# Raft Server Composition

This package provides a composition wrapper for starting a single Raft node. It wires together the metadata service (with Raft consensus), communicator, storage, and other components needed for distributed file storage.

The `Build()` function creates a server instance that can be started with `Run()`. Components can be swapped by modifying the construction logic in `server.go`.

Note: This starts one node - clusters are formed by starting multiple processes with different node IDs and shared seed peers.