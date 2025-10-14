package main

import (
	"flag"
	"log"
	"strings"

	"github.com/AnishMulay/sandstore/servers/raft"
	"github.com/AnishMulay/sandstore/servers/simple"
)

func main() {
	var (
		serverType = flag.String("server", "raft", "Server type (raft)")
		nodeID     = flag.String("node-id", "", "Node ID")
		listen     = flag.String("listen", ":8080", "Listen address")
		dataDir    = flag.String("data-dir", "./data", "Data directory")
		seeds      = flag.String("seeds", "", "Comma-separated seed peers")
		bootstrap  = flag.Bool("bootstrap", false, "Bootstrap cluster")
	)
	flag.Parse()

	if *nodeID == "" {
		log.Fatal("--node-id is required")
	}

	var seedPeers []string
	if *seeds != "" {
		seedPeers = strings.Split(*seeds, ",")
	}

	switch *serverType {
	case "raft":
		opts := raft.Options{
			NodeID:     *nodeID,
			ListenAddr: *listen,
			DataDir:    *dataDir,
			SeedPeers:  seedPeers,
			Bootstrap:  *bootstrap,
		}
		server := raft.Build(opts)
		if err := server.Run(); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	case "simple":
		opts := simple.Options{
			NodeID:     *nodeID,
			ListenAddr: *listen,
			DataDir:    *dataDir,
		}
		server := simple.Build(opts)
		if err := server.Run(); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	default:
		log.Fatalf("Unknown server type: %s", *serverType)
	}
}
