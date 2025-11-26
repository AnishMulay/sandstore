package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	logservice "github.com/AnishMulay/sandstore/internal/log_service"
	locallog "github.com/AnishMulay/sandstore/internal/log_service/localdisc"
	ps "github.com/AnishMulay/sandstore/internal/posix_server"
)

func main() {
	serverAddr := "localhost:9001" // Connect to the first node by default
	filePath := "my-test-file.txt"
	fileData := "Hello from the automated client!"

	ls := locallog.NewLocalDiscLogService("./run/client/logs", "posix-client", logservice.InfoLevel)
	comm := grpccomm.NewGRPCCommunicator(":0", ls)
	registerPayloads(comm)

	ctx := context.Background()

	time.Sleep(time.Second)
	rootID := doLookupPath(ctx, comm, serverAddr, "/")
	log.Printf("--- Starting Test Operations (Discovered Root Inode ID: %s) ---\n", rootID)

	// 1. Create a file
	log.Println("1. Creating file:", filePath)
	createReq := ps.CreateRequest{
		ParentID: rootID,
		Name:     filePath,
		Mode:     0644, UID: 1000, GID: 1000,
	}
	time.Sleep(time.Second)
	sendRequest(ctx, comm, serverAddr, ps.MsgPosixCreate, createReq)

	// We need the new file's Inode ID for subsequent operations
	time.Sleep(time.Second)
	fileID := doLookup(ctx, comm, serverAddr, rootID, filePath)
	log.Printf("   File created with InodeID: %s\n", fileID)

	// 2. Read the empty file
	log.Println("2. Reading the empty file...")
	time.Sleep(time.Second)
	readReqEmpty := ps.ReadRequest{InodeID: fileID, Offset: 0, Length: 1024}
	sendRequest(ctx, comm, serverAddr, ps.MsgPosixRead, readReqEmpty)

	// 3. Write to the file
	log.Printf("3. Writing data: \"%s\"\n", fileData)
	writeReq := ps.WriteRequest{
		InodeID: fileID,
		Offset:  0,
		Data:    []byte(fileData),
	}
	time.Sleep(time.Second)
	sendRequest(ctx, comm, serverAddr, ps.MsgPosixWrite, writeReq)

	// 4. Read the file again to verify content
	log.Println("4. Reading the file again to verify content...")
	time.Sleep(time.Second)
	readReqFull := ps.ReadRequest{InodeID: fileID, Offset: 0, Length: 1024}
	sendRequest(ctx, comm, serverAddr, ps.MsgPosixRead, readReqFull)

	log.Println("--- Test Operations Complete ---")
}

func registerPayloads(comm *grpccomm.GRPCCommunicator) {
	comm.RegisterPayloadType(ps.MsgPosixFsInfo, reflect.TypeOf(ps.FsInfoRequest{}))
	comm.RegisterPayloadType(ps.MsgPosixMkdir, reflect.TypeOf(ps.MkdirRequest{}))
	comm.RegisterPayloadType(ps.MsgPosixCreate, reflect.TypeOf(ps.CreateRequest{}))
	comm.RegisterPayloadType(ps.MsgPosixWrite, reflect.TypeOf(ps.WriteRequest{}))
	comm.RegisterPayloadType(ps.MsgPosixRead, reflect.TypeOf(ps.ReadRequest{}))
	comm.RegisterPayloadType(ps.MsgPosixLookup, reflect.TypeOf(ps.LookupRequest{}))
	comm.RegisterPayloadType(ps.MsgPosixLookupPath, reflect.TypeOf(ps.LookupPathRequest{}))
}

func sendRequest(ctx context.Context, comm *grpccomm.GRPCCommunicator, addr string, msgType string, payload any) {
	msg := communication.Message{From: "client", Type: msgType, Payload: payload}

	for i := 0; i < 2; i++ { // Allow one retry
		log.Printf("   -> Sending %s to %s", msgType, addr)
		resp, err := comm.Send(ctx, addr, msg)
		if err != nil {
			log.Fatalf("Comm Error: %v", err)
		}

		if resp.Code == communication.CodeOK {
			fmt.Printf("Response: %s\n", string(resp.Body))
			return // Success
		}

		if i == 0 {
			// Try to detect a leader hint in the response body (string or JSON).
			if leaderAddr := extractLeaderAddr(resp.Body); leaderAddr != "" {
				log.Printf("   Not the leader. Retrying on hinted leader: %s", leaderAddr)
				addr = leaderAddr
				continue
			}

			var leaderInfo cluster_service.Node
			if err := json.Unmarshal(resp.Body, &leaderInfo); err == nil && leaderInfo.Address != "" {
				log.Printf("   Not the leader. Retrying on hinted leader: %s", leaderInfo.Address)
				addr = leaderInfo.Address
				continue
			}
		}

		log.Fatalf("Server Error for %s (from %s): %s", msgType, addr, string(resp.Body))
	}
}

func doLookup(ctx context.Context, comm *grpccomm.GRPCCommunicator, addr string, parentID, name string) string {
	req := ps.LookupRequest{ParentID: parentID, Name: name}
	msg := communication.Message{From: "client", Type: ps.MsgPosixLookup, Payload: req}
	resp, err := comm.Send(ctx, addr, msg)
	if err != nil || resp == nil || resp.Code != communication.CodeOK {
		body := ""
		if resp != nil {
			body = string(resp.Body)
		}
		log.Fatalf("Lookup failed for '%s': err=%v resp=%s", name, err, body)
	}
	var id string
	json.Unmarshal(resp.Body, &id)
	return id
}

func doLookupPath(ctx context.Context, comm *grpccomm.GRPCCommunicator, addr string, path string) string {
	req := ps.LookupPathRequest{Path: path}
	msg := communication.Message{From: "client", Type: ps.MsgPosixLookupPath, Payload: req}
	resp, err := comm.Send(ctx, addr, msg)
	if err != nil || resp == nil || resp.Code != communication.CodeOK {
		body := ""
		if resp != nil {
			body = string(resp.Body)
		}
		log.Fatalf("LookupPath failed for '%s': err=%v resp=%s", path, err, body)
	}
	var id string
	json.Unmarshal(resp.Body, &id)
	return id
}

// extractLeaderAddr tries to pull a leader address out of an error body.
// It supports the raft error string format: "node is not the leader. leader is X at Y".
func extractLeaderAddr(body []byte) string {
	s := string(body)
	const marker = " at "
	if idx := strings.LastIndex(s, marker); idx != -1 {
		leaderAddr := strings.TrimSpace(s[idx+len(marker):])
		if leaderAddr != "" {
			return leaderAddr
		}
	}
	return ""
}
