package main

import (
	"flag"
	"log"
	"strings"

	"github.com/AnishMulay/sandstore/servers/node"
)

func main() {
	var (
		serverType = flag.String("server", "node", "Server type (node)")
		nodeID     = flag.String("node-id", "", "Node ID")
		listen     = flag.String("listen", ":8080", "Listen address")
		dataDir    = flag.String("data-dir", "./data", "Data directory")
		seeds      = flag.String("seeds", "", "Comma-separated seed peers")
	)
	flag.Parse()

	if *nodeID == "" {
		log.Fatal("--node-id is required")
	}

	var seedPeers []string
	if *seeds != "" {
		seedPeers = strings.Split(*seeds, ",")
	}

	if *serverType != "node" {
		log.Fatalf("Unknown server type: %s", *serverType)
	}

	opts := node.Options{
		NodeID:     *nodeID,
		ListenAddr: *listen,
		DataDir:    *dataDir,
		SeedPeers:  seedPeers,
	}
	server := node.Build(opts)
	if err := server.Run(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
