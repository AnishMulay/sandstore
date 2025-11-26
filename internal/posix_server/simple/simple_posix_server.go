package simple

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/AnishMulay/sandstore/internal/communication"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	"github.com/AnishMulay/sandstore/internal/log_service"
	crep "github.com/AnishMulay/sandstore/internal/posix_chunk_replicator/default_replicator"
	pfs "github.com/AnishMulay/sandstore/internal/posix_file_service"
	raft "github.com/AnishMulay/sandstore/internal/posix_metadata_replicator/raft_replicator"
	ps "github.com/AnishMulay/sandstore/internal/posix_server"
)

type SimplePosixServer struct {
	comm      *grpccomm.GRPCCommunicator
	fs        pfs.PosixFileService
	ls        log_service.LogService
	metaRepl  *raft.RaftPosixMetadataReplicator
	chunkRepl *crep.DefaultPosixChunkReplicator
}

func NewSimplePosixServer(
	comm *grpccomm.GRPCCommunicator,
	fs pfs.PosixFileService,
	ls log_service.LogService,
	metaRepl *raft.RaftPosixMetadataReplicator,
	chunkRepl *crep.DefaultPosixChunkReplicator,
) *SimplePosixServer {
	return &SimplePosixServer{
		comm:      comm,
		fs:        fs,
		ls:        ls,
		metaRepl:  metaRepl,
		chunkRepl: chunkRepl,
	}
}

func (s *SimplePosixServer) Start() error {
	s.ls.Info(log_service.LogEvent{Message: "Starting Simple POSIX Server"})

	// 1. Register Payload Types with Communicator
	s.registerPayloads()

	// 2. Start File Service (which starts Metadata/Chunk services)
	if err := s.fs.Start(); err != nil {
		return err
	}

	// 3. Start Communicator with our central handler
	return s.comm.Start(s.handleMessage)
}

func (s *SimplePosixServer) Stop() error {
	s.ls.Info(log_service.LogEvent{Message: "Stopping Simple POSIX Server"})
	if err := s.fs.Stop(); err != nil {
		s.ls.Error(log_service.LogEvent{Message: "Failed to stop file service", Metadata: map[string]any{"error": err.Error()}})
	}
	return s.comm.Stop()
}

func (s *SimplePosixServer) registerPayloads() {
	// File System Payloads
	s.comm.RegisterPayloadType(ps.MsgPosixGetAttr, reflect.TypeOf(ps.GetAttrRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixSetAttr, reflect.TypeOf(ps.SetAttrRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixLookup, reflect.TypeOf(ps.LookupRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixLookupPath, reflect.TypeOf(ps.LookupPathRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixAccess, reflect.TypeOf(ps.AccessRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixRead, reflect.TypeOf(ps.ReadRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixWrite, reflect.TypeOf(ps.WriteRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixCreate, reflect.TypeOf(ps.CreateRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixMkdir, reflect.TypeOf(ps.MkdirRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixRemove, reflect.TypeOf(ps.RemoveRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixRmdir, reflect.TypeOf(ps.RmdirRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixRename, reflect.TypeOf(ps.RenameRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixReadDir, reflect.TypeOf(ps.ReadDirRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixReadDirPlus, reflect.TypeOf(ps.ReadDirPlusRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixFsStat, reflect.TypeOf(ps.FsStatRequest{}))
	s.comm.RegisterPayloadType(ps.MsgPosixFsInfo, reflect.TypeOf(ps.FsInfoRequest{}))

	// Replicator Payloads
	s.comm.RegisterPayloadType(ps.MsgPosixRaftRequestVote, reflect.TypeOf(raft.RequestVoteArgs{}))
	s.comm.RegisterPayloadType(ps.MsgPosixRaftAppendEntries, reflect.TypeOf(communication.AppendEntriesRequest{}))
}

func (s *SimplePosixServer) RegisterTypedHandler(messageType string, payloadType reflect.Type, handler func(msg communication.Message) (*communication.Response, error)) {
	// This is a placeholder to satisfy the interface.
	// In a real implementation, we might want to allow dynamic registration of handlers.
	// For now, our handleMessage router handles everything.
	s.ls.Warn(log_service.LogEvent{
		Message:  "RegisterTypedHandler called but not implemented (using central router)",
		Metadata: map[string]any{"type": messageType},
	})
}

// Central Router for all incoming messages
func (s *SimplePosixServer) handleMessage(msg communication.Message) (*communication.Response, error) {
	ctx := context.Background()

	switch msg.Type {
	// --- 1. GETATTR ---
	case ps.MsgPosixGetAttr:
		req := msg.Payload.(ps.GetAttrRequest)
		attr, err := s.fs.GetAttr(ctx, req.InodeID)
		return s.respond(attr, err)

	// --- 2. SETATTR ---
	case ps.MsgPosixSetAttr:
		req := msg.Payload.(ps.SetAttrRequest)
		attr, err := s.fs.SetAttr(ctx, req.InodeID, req.Mode, req.UID, req.GID, req.ATime, req.MTime)
		return s.respond(attr, err)

	// --- 3. LOOKUP ---
	case ps.MsgPosixLookup:
		req := msg.Payload.(ps.LookupRequest)
		id, err := s.fs.Lookup(ctx, req.ParentID, req.Name)
		return s.respond(id, err)

	// --- 3a. LOOKUP PATH ---
	case ps.MsgPosixLookupPath:
		req := msg.Payload.(ps.LookupPathRequest)
		id, err := s.fs.LookupPath(ctx, req.Path)
		return s.respond(id, err)

	// --- 4. ACCESS ---
	case ps.MsgPosixAccess:
		req := msg.Payload.(ps.AccessRequest)
		err := s.fs.Access(ctx, req.InodeID, req.UID, req.GID, req.AccessMask)
		// Access returns no data, just success/fail status
		return s.respond(nil, err)

	// --- 5. READ ---
	case ps.MsgPosixRead:
		req := msg.Payload.(ps.ReadRequest)
		data, err := s.fs.Read(ctx, req.InodeID, req.Offset, req.Length)
		return s.respond(data, err)

	// --- 6. WRITE ---
	case ps.MsgPosixWrite:
		req := msg.Payload.(ps.WriteRequest)
		n, err := s.fs.Write(ctx, req.InodeID, req.Offset, req.Data)
		return s.respond(n, err)

	// --- 7. CREATE ---
	case ps.MsgPosixCreate:
		req := msg.Payload.(ps.CreateRequest)
		inode, err := s.fs.Create(ctx, req.ParentID, req.Name, req.Mode, req.UID, req.GID)
		return s.respond(inode, err)

	// --- 8. MKDIR ---
	case ps.MsgPosixMkdir:
		req := msg.Payload.(ps.MkdirRequest)
		inode, err := s.fs.Mkdir(ctx, req.ParentID, req.Name, req.Mode, req.UID, req.GID)
		return s.respond(inode, err)

	// --- 9. REMOVE ---
	case ps.MsgPosixRemove:
		req := msg.Payload.(ps.RemoveRequest)
		err := s.fs.Remove(ctx, req.ParentID, req.Name)
		return s.respond(nil, err)

	// --- 10. RMDIR ---
	case ps.MsgPosixRmdir:
		req := msg.Payload.(ps.RmdirRequest)
		err := s.fs.Rmdir(ctx, req.ParentID, req.Name)
		return s.respond(nil, err)

	// --- 11. RENAME ---
	case ps.MsgPosixRename:
		req := msg.Payload.(ps.RenameRequest)
		err := s.fs.Rename(ctx, req.SrcParentID, req.SrcName, req.DstParentID, req.DstName)
		return s.respond(nil, err)

	// --- 12. READDIR ---
	case ps.MsgPosixReadDir:
		req := msg.Payload.(ps.ReadDirRequest)
		entries, cookie, eof, err := s.fs.ReadDir(ctx, req.InodeID, req.Cookie, req.MaxEntries)
		// Wrap multiple returns in a map
		res := map[string]any{
			"entries": entries,
			"cookie":  cookie,
			"eof":     eof,
		}
		return s.respond(res, err)

	// --- 13. READDIRPLUS ---
	case ps.MsgPosixReadDirPlus:
		req := msg.Payload.(ps.ReadDirPlusRequest)
		entries, cookie, eof, err := s.fs.ReadDirPlus(ctx, req.InodeID, req.Cookie, req.MaxEntries)
		res := map[string]any{
			"entries": entries,
			"cookie":  cookie,
			"eof":     eof,
		}
		return s.respond(res, err)

	// --- 14. FSSTAT ---
	case ps.MsgPosixFsStat:
		stats, err := s.fs.GetFsStat(ctx)
		return s.respond(stats, err)

	// --- 15. FSINFO ---
	case ps.MsgPosixFsInfo:
		info, err := s.fs.GetFsInfo(ctx)
		return s.respond(info, err)

	// --- REPLICATOR OPERATIONS (Raft) ---
	case ps.MsgPosixRaftRequestVote:
		req := msg.Payload.(raft.RequestVoteArgs)
		res, err := s.metaRepl.HandleRequestVote(req)
		return s.respond(res, err)

	case ps.MsgPosixRaftAppendEntries:
		req := msg.Payload.(communication.AppendEntriesRequest)
		res, err := s.metaRepl.HandleAppendEntries(req)
		return s.respond(res, err)

	default:
		return &communication.Response{
			Code: communication.CodeBadRequest,
			Body: []byte("unknown message type: " + msg.Type),
		}, nil
	}
}

// respond is a helper to standardize JSON responses and error codes
func (s *SimplePosixServer) respond(data any, err error) (*communication.Response, error) {
	if err != nil {
		// In a real implementation, we would switch on err types (ErrNotFound, etc.)
		// to return CodeNotFound vs CodeInternal.
		// For simple MVP, we default to Internal for generic errors.
		return &communication.Response{
			Code: communication.CodeInternal,
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
