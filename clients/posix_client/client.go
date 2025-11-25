package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"reflect"

	"github.com/AnishMulay/sandstore/internal/communication"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	logservice "github.com/AnishMulay/sandstore/internal/log_service"
	locallog "github.com/AnishMulay/sandstore/internal/log_service/localdisc"
	ps "github.com/AnishMulay/sandstore/internal/posix_server"
)

func main() {
	serverAddr := flag.String("server", "localhost:9001", "Address of the POSIX server")
	op := flag.String("op", "fsinfo", "Operation: fsinfo, mkdir, create, write, read")
	rootID := flag.String("root", "", "Root Inode ID (required for file ops)")
	path := flag.String("path", "testfile", "Filename (relative to root)")
	data := flag.String("data", "Hello Distributed World", "Data to write")
	flag.Parse()

	ls := locallog.NewLocalDiscLogService("./run/client/logs", "posix-client", logservice.InfoLevel)
	comm := grpccomm.NewGRPCCommunicator(":0", ls)
	registerPayloads(comm)

	ctx := context.Background()

	switch *op {
	case "fsinfo":
		sendRequest(ctx, comm, *serverAddr, ps.MsgPosixFsInfo, ps.FsInfoRequest{})

	case "mkdir":
		if *rootID == "" { log.Fatal("--root is required") }
		req := ps.MkdirRequest{
			ParentID: *rootID,
			Name:     *path,
			Mode:     0755, UID: 1000, GID: 1000,
		}
		sendRequest(ctx, comm, *serverAddr, ps.MsgPosixMkdir, req)

	case "create":
		if *rootID == "" { log.Fatal("--root is required") }
		req := ps.CreateRequest{
			ParentID: *rootID,
			Name:     *path,
			Mode:     0644, UID: 1000, GID: 1000,
		}
		sendRequest(ctx, comm, *serverAddr, ps.MsgPosixCreate, req)

	case "write":
		if *rootID == "" { log.Fatal("--root is required") }
		// 1. Lookup
		fileID := doLookup(ctx, comm, *serverAddr, *rootID, *path)
		// 2. Write
		req := ps.WriteRequest{
			InodeID: fileID,
			Offset:  0,
			Data:    []byte(*data),
		}
		sendRequest(ctx, comm, *serverAddr, ps.MsgPosixWrite, req)

	case "read":
		if *rootID == "" { log.Fatal("--root is required") }
		fileID := doLookup(ctx, comm, *serverAddr, *rootID, *path)
		req := ps.ReadRequest{
			InodeID: fileID,
			Offset:  0,
			Length:  1024,
		}
		sendRequest(ctx, comm, *serverAddr, ps.MsgPosixRead, req)
	}
}

func registerPayloads(comm *grpccomm.GRPCCommunicator) {
	comm.RegisterPayloadType(ps.MsgPosixFsInfo, reflect.TypeOf(ps.FsInfoRequest{}))
	comm.RegisterPayloadType(ps.MsgPosixMkdir, reflect.TypeOf(ps.MkdirRequest{}))
	comm.RegisterPayloadType(ps.MsgPosixCreate, reflect.TypeOf(ps.CreateRequest{}))
	comm.RegisterPayloadType(ps.MsgPosixWrite, reflect.TypeOf(ps.WriteRequest{}))
	comm.RegisterPayloadType(ps.MsgPosixRead, reflect.TypeOf(ps.ReadRequest{}))
	comm.RegisterPayloadType(ps.MsgPosixLookup, reflect.TypeOf(ps.LookupRequest{}))
}

func sendRequest(ctx context.Context, comm *grpccomm.GRPCCommunicator, addr string, msgType string, payload any) {
	msg := communication.Message{From: "client", Type: msgType, Payload: payload}
	resp, err := comm.Send(ctx, addr, msg)
	if err != nil { log.Fatalf("Comm Error: %v", err) }
	if resp.Code != communication.CodeOK { log.Fatalf("Server Error: %s", string(resp.Body)) }
	fmt.Printf("Response: %s\n", string(resp.Body))
}

func doLookup(ctx context.Context, comm *grpccomm.GRPCCommunicator, addr string, parentID, name string) string {
	req := ps.LookupRequest{ParentID: parentID, Name: name}
	msg := communication.Message{From: "client", Type: ps.MsgPosixLookup, Payload: req}
	resp, err := comm.Send(ctx, addr, msg)
	if err != nil || resp.Code != communication.CodeOK { log.Fatalf("Lookup failed for %s", name) }
	var id string
	json.Unmarshal(resp.Body, &id)
	return id
}