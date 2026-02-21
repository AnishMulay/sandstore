package main

import (
	"flag"
	"log"
	"os"
	"strings"

	"github.com/AnishMulay/sandstore/servers/node"
)

func main() {
	nodeIDDefault := os.Getenv("NODE_ID")

	listenDefault := os.Getenv("LISTEN_ADDR")
	if listenDefault == "" {
		listenDefault = ":8080"
	}

	dataDirDefault := os.Getenv("DATA_DIR")
	if dataDirDefault == "" {
		dataDirDefault = "./data"
	}

	var (
		serverType = flag.String("server", "node", "Server type (node)")
		nodeID     = flag.String("node-id", nodeIDDefault, "Node ID")
		listen     = flag.String("listen", listenDefault, "Listen address")
		dataDir    = flag.String("data-dir", dataDirDefault, "Data directory")
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
