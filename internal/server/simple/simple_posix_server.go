package simple

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	pcs "github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	"github.com/AnishMulay/sandstore/internal/log_service"
	raft "github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
	inmemoryms "github.com/AnishMulay/sandstore/internal/metadata_service/inmemory"
	"github.com/AnishMulay/sandstore/internal/orchestrators"
	ps "github.com/AnishMulay/sandstore/internal/server"
)

type SimpleServer struct {
	comm      *grpccomm.GRPCCommunicator
	cpo       orchestrators.ControlPlaneOrchestrator
	dpo       orchestrators.DataPlaneOrchestrator
	cs        pcs.ChunkService
	ls        log_service.LogService
}

func NewSimpleServer(
	comm *grpccomm.GRPCCommunicator,
	cpo orchestrators.ControlPlaneOrchestrator,
	dpo orchestrators.DataPlaneOrchestrator,
	cs pcs.ChunkService,
	ls log_service.LogService,
) *SimpleServer {
	return &SimpleServer{
		comm: comm,
		cpo:  cpo,
		dpo:  dpo,
		cs:   cs,
		ls:   ls,
	}
}

func (s *SimpleServer) Start() error {
	s.ls.Info(log_service.LogEvent{Message: "Starting Simple POSIX Server"})

	// 1. Register Payload Types with Communicator
	s.registerPayloads()

	// 2. Start Control Plane
	if err := s.cpo.Start(); err != nil {
		return err
	}

	// 3. Start Communicator with our central handler
	return s.comm.Start(s.handleMessage)
}

func (s *SimpleServer) Stop() error {
	s.ls.Info(log_service.LogEvent{Message: "Stopping Simple POSIX Server"})
	if err := s.cpo.Stop(); err != nil {
		s.ls.Error(log_service.LogEvent{Message: "Failed to stop control plane", Metadata: map[string]any{"error": err.Error()}})
	}
	return s.comm.Stop()
}

func (s *SimpleServer) registerPayloads() {
	// File System Payloads
	s.comm.RegisterPayloadType(ps.MsgGetAttr, reflect.TypeOf(ps.GetAttrRequest{}))
	s.comm.RegisterPayloadType(ps.MsgSetAttr, reflect.TypeOf(ps.SetAttrRequest{}))
	s.comm.RegisterPayloadType(ps.MsgLookup, reflect.TypeOf(ps.LookupRequest{}))
	s.comm.RegisterPayloadType(ps.MsgLookupPath, reflect.TypeOf(ps.LookupPathRequest{}))
	s.comm.RegisterPayloadType(ps.MsgAccess, reflect.TypeOf(ps.AccessRequest{}))
	s.comm.RegisterPayloadType(ps.MsgRead, reflect.TypeOf(ps.ReadRequest{}))
	s.comm.RegisterPayloadType(ps.MsgWrite, reflect.TypeOf(ps.WriteRequest{}))
	s.comm.RegisterPayloadType(ps.MsgCreate, reflect.TypeOf(ps.CreateRequest{}))
	s.comm.RegisterPayloadType(ps.MsgMkdir, reflect.TypeOf(ps.MkdirRequest{}))
	s.comm.RegisterPayloadType(ps.MsgRemove, reflect.TypeOf(ps.RemoveRequest{}))
	s.comm.RegisterPayloadType(ps.MsgRmdir, reflect.TypeOf(ps.RmdirRequest{}))
	s.comm.RegisterPayloadType(ps.MsgRename, reflect.TypeOf(ps.RenameRequest{}))
	s.comm.RegisterPayloadType(ps.MsgReadDir, reflect.TypeOf(ps.ReadDirRequest{}))
	s.comm.RegisterPayloadType(ps.MsgReadDirPlus, reflect.TypeOf(ps.ReadDirPlusRequest{}))
	s.comm.RegisterPayloadType(ps.MsgFsStat, reflect.TypeOf(ps.FsStatRequest{}))
	s.comm.RegisterPayloadType(ps.MsgFsInfo, reflect.TypeOf(ps.FsInfoRequest{}))

	// Replicator Payloads
	s.comm.RegisterPayloadType(ps.MsgRaftRequestVote, reflect.TypeOf(raft.RequestVoteArgs{}))
	s.comm.RegisterPayloadType(ps.MsgRaftAppendEntries, reflect.TypeOf(communication.AppendEntriesRequest{}))
	s.comm.RegisterPayloadType(ps.MsgRaftInstallSnapshot, reflect.TypeOf(communication.InstallSnapshotRequest{}))

	// Chunk Replication Payloads
	s.comm.RegisterPayloadType(ps.MsgChunkWrite, reflect.TypeOf(communication.WriteChunkRequest{}))
	s.comm.RegisterPayloadType(ps.MsgChunkRead, reflect.TypeOf(communication.ReadChunkRequest{}))
	s.comm.RegisterPayloadType(ps.MsgChunkDelete, reflect.TypeOf(communication.DeleteChunkRequest{}))
	// Also register the communication constants used by the chunk replicator
	s.comm.RegisterPayloadType(communication.MessageTypeWriteChunk, reflect.TypeOf(communication.WriteChunkRequest{}))
	s.comm.RegisterPayloadType(communication.MessageTypeReadChunk, reflect.TypeOf(communication.ReadChunkRequest{}))
	s.comm.RegisterPayloadType(communication.MessageTypeDeleteChunk, reflect.TypeOf(communication.DeleteChunkRequest{}))
	s.comm.RegisterPayloadType(communication.MessageTypePrepareChunk, reflect.TypeOf(communication.PrepareChunkRequest{}))
	s.comm.RegisterPayloadType(communication.MessageTypeCommitChunk, reflect.TypeOf(communication.CommitChunkRequest{}))
	s.comm.RegisterPayloadType(communication.MessageTypeAbortChunk, reflect.TypeOf(communication.AbortChunkRequest{}))
}

func (s *SimpleServer) RegisterTypedHandler(messageType string, payloadType reflect.Type, handler func(msg communication.Message) (*communication.Response, error)) {
	// This is a placeholder to satisfy the interface.
	// In a real implementation, we might want to allow dynamic registration of handlers.
	// For now, our handleMessage router handles everything.
	s.ls.Warn(log_service.LogEvent{
		Message:  "RegisterTypedHandler called but not implemented (using central router)",
		Metadata: map[string]any{"type": messageType},
	})
}

// Central Router for all incoming messages
func (s *SimpleServer) handleMessage(msg communication.Message) (*communication.Response, error) {
	ctx := context.Background()

	switch msg.Type {
	// --- 1. GETATTR ---
	case ps.MsgGetAttr:
		req := msg.Payload.(ps.GetAttrRequest)
		attr, err := s.cpo.GetAttr(ctx, req.InodeID)
		return s.respond(attr, err)

	// --- 2. SETATTR ---
	case ps.MsgSetAttr:
		req := msg.Payload.(ps.SetAttrRequest)
		attr, err := s.cpo.SetAttr(ctx, req.InodeID, req.Mode, req.UID, req.GID, req.ATime, req.MTime)
		return s.respond(attr, err)

	// --- 3. LOOKUP ---
	case ps.MsgLookup:
		req := msg.Payload.(ps.LookupRequest)
		id, err := s.cpo.Lookup(ctx, req.ParentID, req.Name)
		return s.respond(id, err)

	// --- 3a. LOOKUP PATH ---
	case ps.MsgLookupPath:
		req := msg.Payload.(ps.LookupPathRequest)
		id, err := s.cpo.LookupPath(ctx, req.Path)
		return s.respond(id, err)

	// --- 4. ACCESS ---
	case ps.MsgAccess:
		req := msg.Payload.(ps.AccessRequest)
		err := s.cpo.Access(ctx, req.InodeID, req.UID, req.GID, req.AccessMask)
		// Access returns no data, just success/fail status
		return s.respond(nil, err)

	// --- 5. READ ---
	case ps.MsgRead:
		req := msg.Payload.(ps.ReadRequest)
		readCtx, err := s.cpo.PrepareFileRead(ctx, req.InodeID, req.Offset)
		if err != nil {
			return s.respond(nil, err)
		}
		if readCtx == nil || readCtx.ChunkID == "" {
			return s.respond([]byte{}, nil)
		}
		data, err := s.dpo.ExecuteRead(ctx, readCtx.ChunkID, readCtx.TargetNodes)
		return s.respond(data, err)

	// --- 6. WRITE ---
	case ps.MsgWrite:
		req := msg.Payload.(ps.WriteRequest)
		writeCtx, err := s.cpo.PrepareFileWrite(ctx, req.InodeID, req.Offset, int64(len(req.Data)))
		if err != nil {
			return s.respond(int64(0), err)
		}

		err = s.dpo.ExecuteWrite(ctx, writeCtx.TxnID, writeCtx.ChunkID, req.Offset, req.Data, writeCtx.TargetNodes, writeCtx.IsNewChunk)
		if err != nil {
			abortErr := s.cpo.AbortFileWrite(ctx, writeCtx.TxnID, writeCtx.ChunkID, writeCtx.TargetNodes)
			if abortErr != nil {
				return s.respond(int64(0), fmt.Errorf("write failed: %v, AND abort failed: %v", err, abortErr))
			}
			return s.respond(int64(0), err)
		}

		newEOF := req.Offset + int64(len(req.Data))
		err = s.cpo.CommitFileWrite(ctx, writeCtx.TxnID, req.InodeID, writeCtx.ChunkID, newEOF, writeCtx.IsNewChunk, writeCtx.TargetNodes)
		if err != nil {
			abortErr := s.cpo.AbortFileWrite(ctx, writeCtx.TxnID, writeCtx.ChunkID, writeCtx.TargetNodes)
			if abortErr != nil {
				return s.respond(int64(0), fmt.Errorf("commit failed: %v, AND abort failed: %v", err, abortErr))
			}
			return s.respond(int64(0), err)
		}

		return s.respond(int64(len(req.Data)), nil)

	// --- 7. CREATE ---
	case ps.MsgCreate:
		req := msg.Payload.(ps.CreateRequest)
		inode, err := s.cpo.Create(ctx, req.ParentID, req.Name, req.Mode, req.UID, req.GID)
		return s.respond(inode, err)

	// --- 8. MKDIR ---
	case ps.MsgMkdir:
		req := msg.Payload.(ps.MkdirRequest)
		inode, err := s.cpo.Mkdir(ctx, req.ParentID, req.Name, req.Mode, req.UID, req.GID)
		return s.respond(inode, err)

	// --- 9. REMOVE ---
	case ps.MsgRemove:
		req := msg.Payload.(ps.RemoveRequest)
		err := s.cpo.Remove(ctx, req.ParentID, req.Name)
		return s.respond(nil, err)

	// --- 10. RMDIR ---
	case ps.MsgRmdir:
		req := msg.Payload.(ps.RmdirRequest)
		err := s.cpo.Rmdir(ctx, req.ParentID, req.Name)
		return s.respond(nil, err)

	// --- 11. RENAME ---
	case ps.MsgRename:
		req := msg.Payload.(ps.RenameRequest)
		err := s.cpo.Rename(ctx, req.SrcParentID, req.SrcName, req.DstParentID, req.DstName)
		return s.respond(nil, err)

	// --- 12. READDIR ---
	case ps.MsgReadDir:
		req := msg.Payload.(ps.ReadDirRequest)
		entries, cookie, eof, err := s.cpo.ReadDir(ctx, req.InodeID, req.Cookie, req.MaxEntries)
		// Wrap multiple returns in a map
		res := map[string]any{
			"entries": entries,
			"cookie":  cookie,
			"eof":     eof,
		}
		return s.respond(res, err)

	// --- 13. READDIRPLUS ---
	case ps.MsgReadDirPlus:
		req := msg.Payload.(ps.ReadDirPlusRequest)
		entries, cookie, eof, err := s.cpo.ReadDirPlus(ctx, req.InodeID, req.Cookie, req.MaxEntries)
		res := map[string]any{
			"entries": entries,
			"cookie":  cookie,
			"eof":     eof,
		}
		return s.respond(res, err)

	// --- 14. FSSTAT ---
	case ps.MsgFsStat:
		stats, err := s.cpo.GetFsStat(ctx)
		return s.respond(stats, err)

	// --- 15. FSINFO ---
	case ps.MsgFsInfo:
		info, err := s.cpo.GetFsInfo(ctx)
		return s.respond(info, err)

	// --- CHUNK REPLICATION (LOCAL ONLY) ---
	case communication.MessageTypePrepareChunk:
		req := msg.Payload.(communication.PrepareChunkRequest)
		err := s.cs.PrepareChunk(ctx, req.TxnID, req.ChunkID, req.Data, req.Checksum)
		return s.respond(nil, err)

	case communication.MessageTypeCommitChunk:
		req := msg.Payload.(communication.CommitChunkRequest)
		err := s.cs.CommitChunk(ctx, req.TxnID, req.ChunkID)
		return s.respond(nil, err)

	case communication.MessageTypeAbortChunk:
		req := msg.Payload.(communication.AbortChunkRequest)
		err := s.cs.AbortChunk(ctx, req.TxnID, req.ChunkID)
		return s.respond(nil, err)

	case ps.MsgChunkWrite:
		req := msg.Payload.(communication.WriteChunkRequest)
		txnID := "legacy-" + req.ChunkID + "-" + time.Now().Format("20060102150405.000000000")
		checksum := checksumHex(req.Data)
		err := s.cs.PrepareChunk(ctx, txnID, req.ChunkID, req.Data, checksum)
		if err == nil {
			err = s.cs.CommitChunk(ctx, txnID, req.ChunkID)
		}
		return s.respond(nil, err)

	case ps.MsgChunkRead:
		req := msg.Payload.(communication.ReadChunkRequest)
		data, err := s.cs.ReadChunk(ctx, req.ChunkID)
		if err != nil {
			return s.respond(nil, err)
		}
		return &communication.Response{Code: communication.CodeOK, Body: data}, nil

	case ps.MsgChunkDelete:
		req := msg.Payload.(communication.DeleteChunkRequest)
		err := s.cs.DeleteChunkLocal(ctx, req.ChunkID)
		if err != nil {
			return s.respond(nil, err)
		}
		return s.respond(nil, nil)

	// --- REPLICATOR OPERATIONS (Raft) ---
	case ps.MsgRaftRequestVote:
		req := msg.Payload.(raft.RequestVoteArgs)
		res, err := s.cpo.HandleConsensusRequestVote(ctx, req)
		return s.respond(res, err)

	case ps.MsgRaftAppendEntries:
		req := msg.Payload.(communication.AppendEntriesRequest)
		res, err := s.cpo.HandleConsensusAppendEntries(ctx, req)
		return s.respond(res, err)

	case ps.MsgRaftInstallSnapshot:
		req := msg.Payload.(communication.InstallSnapshotRequest)
		res, err := s.cpo.HandleConsensusInstallSnapshot(ctx, req)
		return s.respond(res, err)

	default:
		return &communication.Response{
			Code: communication.CodeBadRequest,
			Body: []byte("unknown message type: " + msg.Type),
		}, nil
	}
}

func checksumHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// respond is a helper to standardize JSON responses and error codes
func (s *SimpleServer) respond(data any, err error) (*communication.Response, error) {
	if err != nil {
		code := communication.CodeInternal
		switch {
		case errors.Is(err, inmemoryms.ErrAlreadyExists):
			code = communication.CodeAlreadyExists
		case errors.Is(err, inmemoryms.ErrNotFound):
			code = communication.CodeNotFound
		}

		return &communication.Response{
			Code: code,
			Body: []byte(err.Error()),
		}, nil
	}

	if data == nil {
		return &communication.Response{Code: communication.CodeOK}, nil
	}

	bytes, marshalErr := json.Marshal(data)
	if marshalErr != nil {
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte("failed to marshal response: " + marshalErr.Error()),
		}, nil
	}

	return &communication.Response{
		Code: communication.CodeOK,
		Body: bytes,
	}, nil
}
