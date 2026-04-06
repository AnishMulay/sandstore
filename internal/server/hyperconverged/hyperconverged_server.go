package hyperconverged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	clienttopology "github.com/AnishMulay/sandstore/clients/library/topology"
	"github.com/AnishMulay/sandstore/internal/communication"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	"github.com/AnishMulay/sandstore/internal/log_service"
	raft "github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
	inmemoryms "github.com/AnishMulay/sandstore/internal/metadata_service/inmemory"
	"github.com/AnishMulay/sandstore/internal/metrics"
	"github.com/AnishMulay/sandstore/internal/orchestrators/protocol"
	ps "github.com/AnishMulay/sandstore/internal/server"
	"github.com/AnishMulay/sandstore/topology/contract"
)

type TopologyProvider interface {
	GetLeaderAddress() string
}

type consensusHandler interface {
	HandleConsensusRequestVote(ctx context.Context, req raft.RequestVoteArgs) (*raft.RequestVoteReply, error)
	HandleConsensusAppendEntries(ctx context.Context, req protocol.AppendEntriesRequest) (*raft.AppendEntriesReply, error)
	HandleConsensusInstallSnapshot(ctx context.Context, req protocol.InstallSnapshotRequest) (*raft.InstallSnapshotReply, error)
}

type HyperconvergedServer struct {
	comm             *grpccomm.GRPCCommunicator
	cpo              contract.ControlPlaneOrchestrator
	consensusHandler consensusHandler
	dpo              contract.DataPlaneOrchestrator
	ls               log_service.LogService
	topology         TopologyProvider
	metricsService   metrics.MetricsService
}

func NewHyperconvergedServer(
	comm *grpccomm.GRPCCommunicator,
	cpo contract.ControlPlaneOrchestrator,
	consensusHandler consensusHandler,
	dpo contract.DataPlaneOrchestrator,
	ls log_service.LogService,
	topology TopologyProvider,
	metricsService metrics.MetricsService,
) *HyperconvergedServer {
	return &HyperconvergedServer{
		comm:             comm,
		cpo:              cpo,
		consensusHandler: consensusHandler,
		dpo:              dpo,
		ls:               ls,
		topology:         topology,
		metricsService:   metricsService,
	}
}

func (s *HyperconvergedServer) Start() error {
	s.ls.Info(log_service.LogEvent{Message: "Starting Hyperconverged POSIX Server"})

	// 1. Register Payload Types with Communicator
	s.registerPayloads()

	// 2. Start Control Plane
	if err := s.cpo.Start(); err != nil {
		return err
	}

	// 3. Start Communicator with our central handler
	return s.comm.Start(s.handleMessage)
}

func (s *HyperconvergedServer) Stop() error {
	s.ls.Info(log_service.LogEvent{Message: "Stopping Hyperconverged POSIX Server"})
	if err := s.cpo.Stop(); err != nil {
		s.ls.Error(log_service.LogEvent{Message: "Failed to stop control plane", Metadata: map[string]any{"error": err.Error()}})
	}
	return s.comm.Stop()
}

func (s *HyperconvergedServer) registerPayloads() {
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
	s.comm.RegisterPayloadType(ps.MsgTopologyRequest, reflect.TypeOf(clienttopology.MsgTopologyRequest{}))

	// Replicator Payloads
	s.comm.RegisterPayloadType(ps.MsgRaftRequestVote, reflect.TypeOf(raft.RequestVoteArgs{}))
	s.comm.RegisterPayloadType(ps.MsgRaftAppendEntries, reflect.TypeOf(protocol.AppendEntriesRequest{}))
	s.comm.RegisterPayloadType(ps.MsgRaftInstallSnapshot, reflect.TypeOf(protocol.InstallSnapshotRequest{}))

	// Chunk Replication Payloads
	s.comm.RegisterPayloadType(ps.MsgChunkRead, reflect.TypeOf(communication.ReadChunkRequest{}))
	s.comm.RegisterPayloadType(ps.MsgChunkDelete, reflect.TypeOf(communication.DeleteChunkRequest{}))
	// Also register the communication constants used by chunk handling.
	s.comm.RegisterPayloadType(communication.MessageTypeReadChunk, reflect.TypeOf(communication.ReadChunkRequest{}))
	s.comm.RegisterPayloadType(communication.MessageTypeDeleteChunk, reflect.TypeOf(communication.DeleteChunkRequest{}))
	s.comm.RegisterPayloadType(communication.MessageTypePrepareChunk, reflect.TypeOf(communication.PrepareChunkRequest{}))
	s.comm.RegisterPayloadType(communication.MessageTypeCommitChunk, reflect.TypeOf(communication.CommitChunkRequest{}))
	s.comm.RegisterPayloadType(communication.MessageTypeAbortChunk, reflect.TypeOf(communication.AbortChunkRequest{}))
}

// Central Router for all incoming messages
func (s *HyperconvergedServer) handleMessage(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	start := time.Now()
	var operation string
	defer func() {
		if s == nil || s.metricsService == nil || operation == "" {
			return
		}
		elapsed := time.Since(start).Seconds()
		s.metricsService.Observe(metrics.HyperconvergedServerHandleMessageLatency, elapsed, metrics.MetricTags{
			Operation:  operation,
			Service:    "HyperconvergedServer",
			Additional: nil,
		})
	}()

	switch msg.Type {
	case ps.MsgGetAttr:
		operation = "handle_get_attr"
		return s.handleGetAttr(ctx, msg)
	case ps.MsgSetAttr:
		operation = "handle_set_attr"
		return s.handleSetAttr(ctx, msg)
	case ps.MsgLookup:
		operation = "handle_lookup"
		return s.handleLookup(ctx, msg)
	case ps.MsgLookupPath:
		operation = "handle_lookup_path"
		return s.handleLookupPath(ctx, msg)
	case ps.MsgAccess:
		operation = "handle_access"
		return s.handleAccess(ctx, msg)
	case ps.MsgRead:
		operation = "handle_read"
		return s.handleRead(ctx, msg)
	case ps.MsgWrite:
		operation = "handle_write"
		return s.handleWrite(ctx, msg)
	case ps.MsgCreate:
		operation = "handle_create"
		return s.handleCreate(ctx, msg)
	case ps.MsgMkdir:
		operation = "handle_mkdir"
		return s.handleMkdir(ctx, msg)
	case ps.MsgRemove:
		operation = "handle_remove"
		return s.handleRemove(ctx, msg)
	case ps.MsgRmdir:
		operation = "handle_rmdir"
		return s.handleRmdir(ctx, msg)
	case ps.MsgRename:
		operation = "handle_rename"
		return s.handleRename(ctx, msg)
	case ps.MsgReadDir:
		operation = "handle_read_dir"
		return s.handleReadDir(ctx, msg)
	case ps.MsgReadDirPlus:
		operation = "handle_read_dir_plus"
		return s.handleReadDirPlus(ctx, msg)
	case ps.MsgFsStat:
		operation = "handle_fs_stat"
		return s.handleFsStat(ctx, msg)
	case ps.MsgFsInfo:
		operation = "handle_fs_info"
		return s.handleFsInfo(ctx, msg)
	case ps.MsgTopologyRequest:
		operation = "handle_topology_request"
		return s.handleTopologyRequest(ctx, msg)
	case communication.MessageTypePrepareChunk:
		operation = "handle_prepare_chunk"
		return s.handlePrepareChunk(ctx, msg)
	case communication.MessageTypeCommitChunk:
		operation = "handle_commit_chunk"
		return s.handleCommitChunk(ctx, msg)
	case communication.MessageTypeAbortChunk:
		operation = "handle_abort_chunk"
		return s.handleAbortChunk(ctx, msg)
	case ps.MsgChunkRead:
		operation = "handle_chunk_read"
		return s.handleChunkRead(ctx, msg)
	case ps.MsgChunkDelete:
		operation = "handle_chunk_delete"
		return s.handleChunkDelete(ctx, msg)
	case ps.MsgRaftRequestVote:
		operation = "handle_raft_request_vote"
		return s.handleRaftRequestVote(ctx, msg)
	case ps.MsgRaftAppendEntries:
		operation = "handle_raft_append_entries"
		return s.handleRaftAppendEntries(ctx, msg)
	case ps.MsgRaftInstallSnapshot:
		operation = "handle_raft_install_snapshot"
		return s.handleRaftInstallSnapshot(ctx, msg)
	default:
		return &communication.Response{
			Code: communication.CodeBadRequest,
			Body: []byte("unknown message type: " + msg.Type),
		}, nil
	}
}

func (s *HyperconvergedServer) handleGetAttr(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(ps.GetAttrRequest)
	attr, err := s.cpo.GetAttr(ctx, req.InodeID)
	return s.respond(attr, err)
}

func (s *HyperconvergedServer) handleSetAttr(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(ps.SetAttrRequest)
	attr, err := s.cpo.SetAttr(ctx, req.InodeID, req.Mode, req.UID, req.GID, req.ATime, req.MTime)
	return s.respond(attr, err)
}

func (s *HyperconvergedServer) handleLookup(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(ps.LookupRequest)
	id, err := s.cpo.Lookup(ctx, req.ParentID, req.Name)
	return s.respond(id, err)
}

func (s *HyperconvergedServer) handleLookupPath(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(ps.LookupPathRequest)
	id, err := s.cpo.LookupPath(ctx, req.Path)
	return s.respond(id, err)
}

func (s *HyperconvergedServer) handleAccess(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(ps.AccessRequest)
	err := s.cpo.Access(ctx, req.InodeID, req.UID, req.GID, req.AccessMask)
	return s.respond(nil, err)
}

func (s *HyperconvergedServer) handleRead(ctx context.Context, msg communication.Message) (*communication.Response, error) {
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
}

func (s *HyperconvergedServer) handleWrite(ctx context.Context, msg communication.Message) (*communication.Response, error) {
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
}

func (s *HyperconvergedServer) handleCreate(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(ps.CreateRequest)
	inode, err := s.cpo.Create(ctx, req.ParentID, req.Name, req.Mode, req.UID, req.GID)
	return s.respond(inode, err)
}

func (s *HyperconvergedServer) handleMkdir(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(ps.MkdirRequest)
	inode, err := s.cpo.Mkdir(ctx, req.ParentID, req.Name, req.Mode, req.UID, req.GID)
	return s.respond(inode, err)
}

func (s *HyperconvergedServer) handleRemove(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(ps.RemoveRequest)
	err := s.cpo.Remove(ctx, req.ParentID, req.Name)
	return s.respond(nil, err)
}

func (s *HyperconvergedServer) handleRmdir(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(ps.RmdirRequest)
	err := s.cpo.Rmdir(ctx, req.ParentID, req.Name)
	return s.respond(nil, err)
}

func (s *HyperconvergedServer) handleRename(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(ps.RenameRequest)
	err := s.cpo.Rename(ctx, req.SrcParentID, req.SrcName, req.DstParentID, req.DstName)
	return s.respond(nil, err)
}

func (s *HyperconvergedServer) handleReadDir(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(ps.ReadDirRequest)
	entries, cookie, eof, err := s.cpo.ReadDir(ctx, req.InodeID, req.Cookie, req.MaxEntries)
	res := map[string]any{
		"entries": entries,
		"cookie":  cookie,
		"eof":     eof,
	}
	return s.respond(res, err)
}

func (s *HyperconvergedServer) handleReadDirPlus(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(ps.ReadDirPlusRequest)
	entries, cookie, eof, err := s.cpo.ReadDirPlus(ctx, req.InodeID, req.Cookie, req.MaxEntries)
	res := map[string]any{
		"entries": entries,
		"cookie":  cookie,
		"eof":     eof,
	}
	return s.respond(res, err)
}

func (s *HyperconvergedServer) handleFsStat(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	stats, err := s.cpo.GetFsStat(ctx)
	return s.respond(stats, err)
}

func (s *HyperconvergedServer) handleFsInfo(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	info, err := s.cpo.GetFsInfo(ctx)
	return s.respond(info, err)
}

func (s *HyperconvergedServer) handleTopologyRequest(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	leaderAddr := s.topology.GetLeaderAddress()
	body, err := json.Marshal(clienttopology.MsgTopologyResponse{
		TopologyData: []byte(leaderAddr),
	})
	if err != nil {
		return &communication.Response{
			Code: communication.CodeInternal,
			Body: []byte("failed to marshal response: " + err.Error()),
		}, nil
	}
	return &communication.Response{
		Code: communication.CodeOK,
		Body: body,
	}, nil
}

func (s *HyperconvergedServer) handlePrepareChunk(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(communication.PrepareChunkRequest)
	err := s.dpo.HandlePrepareChunk(ctx, req.TxnID, req.ChunkID, req.Data, req.Checksum)
	return s.respond(nil, err)
}

func (s *HyperconvergedServer) handleCommitChunk(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(communication.CommitChunkRequest)
	err := s.dpo.HandleCommitChunk(ctx, req.TxnID, req.ChunkID)
	return s.respond(nil, err)
}

func (s *HyperconvergedServer) handleAbortChunk(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(communication.AbortChunkRequest)
	err := s.dpo.HandleAbortChunk(ctx, req.TxnID, req.ChunkID)
	return s.respond(nil, err)
}

func (s *HyperconvergedServer) handleChunkRead(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(communication.ReadChunkRequest)
	data, err := s.dpo.HandleReadChunk(ctx, req.ChunkID)
	if err != nil {
		return s.respond(nil, err)
	}
	return &communication.Response{Code: communication.CodeOK, Body: data}, nil
}

func (s *HyperconvergedServer) handleChunkDelete(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(communication.DeleteChunkRequest)
	err := s.dpo.HandleDeleteChunk(ctx, req.ChunkID)
	if err != nil {
		return s.respond(nil, err)
	}
	return s.respond(nil, nil)
}

func (s *HyperconvergedServer) handleRaftRequestVote(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(raft.RequestVoteArgs)
	res, err := s.consensusHandler.HandleConsensusRequestVote(ctx, req)
	return s.respond(res, err)
}

func (s *HyperconvergedServer) handleRaftAppendEntries(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(protocol.AppendEntriesRequest)
	res, err := s.consensusHandler.HandleConsensusAppendEntries(ctx, req)
	return s.respond(res, err)
}

func (s *HyperconvergedServer) handleRaftInstallSnapshot(ctx context.Context, msg communication.Message) (*communication.Response, error) {
	req := msg.Payload.(protocol.InstallSnapshotRequest)
	res, err := s.consensusHandler.HandleConsensusInstallSnapshot(ctx, req)
	return s.respond(res, err)
}

// respond is a helper to standardize JSON responses and error codes
func (s *HyperconvergedServer) respond(data any, err error) (*communication.Response, error) {
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
