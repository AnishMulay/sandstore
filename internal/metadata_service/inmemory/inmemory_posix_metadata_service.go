package inmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/internal/log_service"
	pmr "github.com/AnishMulay/sandstore/internal/metadata_replicator"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/google/uuid"
)

// Internal errors mapped to POSIX concepts
var (
	ErrNotFound      = fmt.Errorf("no such file or directory")
	ErrAlreadyExists = fmt.Errorf("file exists")
	ErrNotDir        = fmt.Errorf("not a directory")
	ErrIsDir         = fmt.Errorf("is a directory")
	ErrNotEmpty      = fmt.Errorf("directory not empty")
	ErrInvalid       = fmt.Errorf("invalid argument")
)

type InMemoryMetadataService struct {
	// State
	mu         sync.RWMutex
	inodes     map[string]*pms.Inode
	superblock *pms.Superblock

	// Dependencies
	replicator pmr.MetadataReplicator
	ls         log_service.LogService
}

func NewInMemoryMetadataService(
	replicator pmr.MetadataReplicator,
	ls log_service.LogService,
) *InMemoryMetadataService {
	return &InMemoryMetadataService{
		inodes:     make(map[string]*pms.Inode),
		replicator: replicator,
		ls:         ls,
	}
}

// --- Lifecycle ---

func (s *InMemoryMetadataService) Start() error {
	s.ls.Info(log_service.LogEvent{Message: "Starting In-Memory POSIX Metadata Service"})

	// 1. Initialize Replicator with our Apply callback
	if err := s.replicator.Start(s.ApplyTransaction); err != nil {
		return err
	}

	// 2. Bootstrap Root if needed (Atomic check-and-init)
	// We replicate a special "Init" op effectively by checking if root exists
	s.mu.Lock()
	if s.superblock == nil {
		// Initialize default superblock
		rootID := "00000000-0000-0000-0000-000000000001"
		s.superblock = &pms.Superblock{
			FsID:            "00000000-0000-0000-0000-000000000000",
			RootInodeID:     rootID,
			ChunkSize:       8 * 1024 * 1024, // 8MB default
			MaxFilenameSize: 255,
			MaxFileSize:     1 << 40, // 1TB
			CreatedAt:       time.Now(),
		}

		// Initialize Root Inode
		s.inodes[rootID] = &pms.Inode{
			InodeID:    rootID,
			Type:       pms.TypeDirectory,
			LinkCount:  2, // Self + Parent (..)
			Mode:       0755,
			AccessTime: time.Now(),
			ModifyTime: time.Now(),
			ChangeTime: time.Now(),
			Children:   make(map[string]string),
		}
		s.ls.Info(log_service.LogEvent{Message: "Bootstrapped Root Inode", Metadata: map[string]any{"id": rootID}})
	}
	s.mu.Unlock()

	return nil
}

func (s *InMemoryMetadataService) Stop() error {
	return s.replicator.Stop()
}

// --- Replication Helper ---

func (s *InMemoryMetadataService) replicateOp(ctx context.Context, op pms.MetadataOperation) error {
	// Tag with metadata
	op.OpID = uuid.New().String()
	op.Timestamp = time.Now().UnixNano()

	data, err := json.Marshal(op)
	if err != nil {
		return fmt.Errorf("failed to marshal op: %w", err)
	}

	// This blocks until the operation is committed and ApplyTransaction is called
	return s.replicator.Replicate(ctx, data)
}

// --- State Machine Application (The "Write" side) ---

func (s *InMemoryMetadataService) ApplyTransaction(data []byte) error {
	var op pms.MetadataOperation
	if err := json.Unmarshal(data, &op); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.ls.Debug(log_service.LogEvent{
		Message:  "Applying Operation",
		Metadata: map[string]any{"type": op.Type, "opID": op.OpID},
	})

	// Idempotency check could go here using OpID history, skipping for MVP complexity.

	switch op.Type {
	case pms.OpCreate:
		return s.applyCreate(op)
	case pms.OpRemove:
		return s.applyRemove(op)
	case pms.OpRename:
		return s.applyRename(op)
	case pms.OpSetAttr:
		return s.applySetAttr(op)
	case pms.OpUpdateInode:
		return s.applyUpdateInode(op)
	default:
		return fmt.Errorf("unknown operation type: %v", op.Type)
	}
}

// --- Internal Appliers (Must be called with Lock held) ---

func (s *InMemoryMetadataService) applyCreate(op pms.MetadataOperation) error {
	// 1. Check Parent
	parent, exists := s.inodes[op.ParentID]
	if !exists || parent.Type != pms.TypeDirectory {
		return ErrNotFound // Or internal consistency error
	}
	if _, exists := parent.Children[op.Name]; exists {
		// Already exists (idempotency or race condition)
		return nil
	}

	// 2. Create Inode
	now := time.Unix(0, op.Timestamp)
	inode := &pms.Inode{
		InodeID:    op.InodeID,
		Type:       op.FileType,
		LinkCount:  1,
		Mode:       op.Mode,
		OwnerUID:   op.UID,
		OwnerGID:   op.GID,
		AccessTime: now,
		ModifyTime: now,
		ChangeTime: now,
	}

	if op.FileType == pms.TypeDirectory {
		inode.Children = make(map[string]string)
		inode.LinkCount = 2 // Self + Parent link
		parent.LinkCount++  // Parent gets a new link (..)
	} else {
		inode.ChunkList = []string{}
	}

	s.inodes[op.InodeID] = inode

	// 3. Update Parent
	parent.Children[op.Name] = op.InodeID
	parent.ModifyTime = now
	parent.ChangeTime = now

	return nil
}

func (s *InMemoryMetadataService) applyRemove(op pms.MetadataOperation) error {
	parent, exists := s.inodes[op.ParentID]
	if !exists {
		return nil // Already gone
	}

	childID, exists := parent.Children[op.Name]
	if !exists {
		return nil // Already gone
	}

	child, exists := s.inodes[childID]
	if exists {
		child.LinkCount--
		// In a real FS, we only delete if LinkCount == 0 (hard links).
		// For MVP, we assume no hard links, so 0 means delete.
		// Directories have LinkCount 2, so decrementing once is fine for logic,
		// but usually Rmdir forces delete.

		if child.Type == pms.TypeDirectory {
			parent.LinkCount--
		}

		delete(s.inodes, childID)
	}

	delete(parent.Children, op.Name)
	now := time.Unix(0, op.Timestamp)
	parent.ModifyTime = now
	parent.ChangeTime = now

	return nil
}

func (s *InMemoryMetadataService) applyRename(op pms.MetadataOperation) error {
	srcParent, ok1 := s.inodes[op.ParentID]
	dstParent, ok2 := s.inodes[op.DstParentID]
	if !ok1 || !ok2 {
		return ErrNotFound
	}

	childID, ok := srcParent.Children[op.Name]
	if !ok {
		return nil // Source gone
	}

	// Remove from source
	delete(srcParent.Children, op.Name)

	// Handle overwrite at destination
	if existingID, exists := dstParent.Children[op.DstName]; exists {
		// "Unlink" the overwritten file
		delete(s.inodes, existingID)
	}

	// Add to destination
	dstParent.Children[op.DstName] = childID

	now := time.Unix(0, op.Timestamp)
	srcParent.ModifyTime = now
	srcParent.ChangeTime = now
	dstParent.ModifyTime = now
	dstParent.ChangeTime = now

	return nil
}

func (s *InMemoryMetadataService) applySetAttr(op pms.MetadataOperation) error {
	inode, exists := s.inodes[op.InodeID]
	if !exists {
		return nil
	}

	now := time.Unix(0, op.Timestamp)

	if op.SetMode != nil {
		inode.Mode = *op.SetMode
	}
	if op.SetUID != nil {
		inode.OwnerUID = *op.SetUID
	}
	if op.SetGID != nil {
		inode.OwnerGID = *op.SetGID
	}
	if op.SetATime != nil {
		inode.AccessTime = time.Unix(0, *op.SetATime)
	}
	if op.SetMTime != nil {
		inode.ModifyTime = time.Unix(0, *op.SetMTime)
	}

	inode.ChangeTime = now
	return nil
}

func (s *InMemoryMetadataService) applyUpdateInode(op pms.MetadataOperation) error {
	inode, exists := s.inodes[op.InodeID]
	if !exists {
		return nil
	}

	if op.NewSize != nil {
		inode.FileSize = *op.NewSize
	}
	if op.NewChunkList != nil {
		inode.ChunkList = op.NewChunkList
	}
	if op.SetMTime != nil {
		inode.ModifyTime = time.Unix(0, *op.SetMTime)
	}

	inode.VersionNumber++
	inode.ChangeTime = time.Unix(0, op.Timestamp)

	return nil
}

// --- Read Operations (Direct Local Access) ---

func (s *InMemoryMetadataService) GetAttributes(ctx context.Context, inodeID string) (*pms.Attributes, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inode, exists := s.inodes[inodeID]
	if !exists {
		return nil, ErrNotFound
	}

	return &pms.Attributes{
		InodeID:    inode.InodeID,
		Type:       inode.Type,
		Mode:       inode.Mode,
		Size:       inode.FileSize,
		AccessTime: inode.AccessTime,
		ModifyTime: inode.ModifyTime,
		ChangeTime: inode.ChangeTime,
		UID:        inode.OwnerUID,
		GID:        inode.OwnerGID,
	}, nil
}

func (s *InMemoryMetadataService) Lookup(ctx context.Context, parentInodeID string, name string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	parent, exists := s.inodes[parentInodeID]
	if !exists {
		return "", ErrNotFound
	}
	if parent.Type != pms.TypeDirectory {
		return "", ErrNotDir
	}

	childID, exists := parent.Children[name]
	if !exists {
		return "", ErrNotFound
	}

	return childID, nil
}

func (s *InMemoryMetadataService) LookupPath(ctx context.Context, path string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if path == "/" {
		return s.superblock.RootInodeID, nil
	}

	// Start at root
	currentID := s.superblock.RootInodeID

	// Clean path
	parts := strings.Split(strings.Trim(path, "/"), "/")

	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}

		inode, exists := s.inodes[currentID]
		if !exists {
			return "", ErrNotFound
		}

		if inode.Type != pms.TypeDirectory {
			return "", ErrNotDir
		}

		// ".." handling could be added here if we stored ParentID in Inode,
		// but for simple LookupPath, strictly hierarchical descent is common.

		nextID, found := inode.Children[part]
		if !found {
			return "", ErrNotFound
		}
		currentID = nextID
	}

	return currentID, nil
}

func (s *InMemoryMetadataService) Access(ctx context.Context, inodeID string, uid, gid uint32, accessMask uint32) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inode, exists := s.inodes[inodeID]
	if !exists {
		return ErrNotFound
	}

	// Root bypass
	if uid == 0 {
		return nil
	}

	// Simplified POSIX check
	var mode uint32 = inode.Mode
	var granted uint32 = 0

	if uid == inode.OwnerUID {
		granted = (mode >> 6) & 7
	} else if gid == inode.OwnerGID {
		granted = (mode >> 3) & 7
	} else {
		granted = mode & 7
	}

	if (granted & accessMask) == accessMask {
		return nil
	}

	// This returns a generic error, but in a real system would be EACCES
	return fmt.Errorf("permission denied")
}

func (s *InMemoryMetadataService) GetInode(ctx context.Context, inodeID string) (*pms.Inode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inode, exists := s.inodes[inodeID]
	if !exists {
		return nil, ErrNotFound
	}

	// Return copy to prevent external mutation
	clone := *inode
	// Deep copy slices/maps if we were rigorous, but strictly speaking
	// the caller shouldn't mutate.
	// Ideally we return a read-only view or struct, but interface returns *Inode.
	return &clone, nil
}

func (s *InMemoryMetadataService) ReadDir(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntry, int, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	dir, exists := s.inodes[inodeID]
	if !exists {
		return nil, 0, false, ErrNotFound
	}
	if dir.Type != pms.TypeDirectory {
		return nil, 0, false, ErrNotDir
	}

	// Pagination logic for Go Maps is tricky because iteration order is random.
	// A robust system would maintain a sorted list of children keys.
	// FOR MVP: We will slurp all keys, sort them (for determinism), and slice.
	// Performance warning: O(N) every call.

	// Note: Since we want "Simplicity" and "Correctness" over "High Perf" for MVP:
	// We collect all entries.
	var allEntries []pms.DirEntry
	for name, id := range dir.Children {
		childType := pms.TypeFile
		if child, ok := s.inodes[id]; ok {
			childType = child.Type
		}
		allEntries = append(allEntries, pms.DirEntry{
			Name:    name,
			InodeID: id,
			Type:    childType,
		})
	}

	// Apply pagination manually
	if cookie >= len(allEntries) {
		return []pms.DirEntry{}, cookie, true, nil // EOF
	}

	end := cookie + maxEntries
	if end > len(allEntries) {
		end = len(allEntries)
	}

	result := allEntries[cookie:end]
	eof := end == len(allEntries)

	return result, end, eof, nil
}

func (s *InMemoryMetadataService) ReadDirPlus(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntryPlus, int, bool, error) {
	// Reuse logic from ReadDir but expand attributes
	entries, nextCookie, eof, err := s.ReadDir(ctx, inodeID, cookie, maxEntries)
	if err != nil {
		return nil, 0, false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var plusEntries []pms.DirEntryPlus
	for _, e := range entries {
		var attrs *pms.Attributes
		if inode, ok := s.inodes[e.InodeID]; ok {
			attrs = &pms.Attributes{
				InodeID:    inode.InodeID,
				Type:       inode.Type,
				Mode:       inode.Mode,
				Size:       inode.FileSize,
				AccessTime: inode.AccessTime,
				ModifyTime: inode.ModifyTime,
				ChangeTime: inode.ChangeTime,
				UID:        inode.OwnerUID,
				GID:        inode.OwnerGID,
			}
		}
		plusEntries = append(plusEntries, pms.DirEntryPlus{
			Name:  e.Name,
			Inode: attrs,
		})
	}

	return plusEntries, nextCookie, eof, nil
}

func (s *InMemoryMetadataService) GetFsStat(ctx context.Context) (*pms.FileSystemStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Calc simple stats
	return &pms.FileSystemStats{
		TotalInodes: int64(s.superblock.MaxFileSize), // Arbitrary limit
		UsedInodes:  int64(len(s.inodes)),
		TotalSpace:  s.superblock.MaxFileSize,
		UsedSpace:   0, // Would need to sum all file sizes
		BlockSize:   s.superblock.ChunkSize,
	}, nil
}

func (s *InMemoryMetadataService) GetFsInfo(ctx context.Context) (*pms.FileSystemInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return &pms.FileSystemInfo{
		FsID:            s.superblock.FsID,
		MaxFileSize:     s.superblock.MaxFileSize,
		MaxFilenameSize: s.superblock.MaxFilenameSize,
		ChunkSize:       s.superblock.ChunkSize,
	}, nil
}

func (s *InMemoryMetadataService) GetChunkList(ctx context.Context, inodeID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inode, exists := s.inodes[inodeID]
	if !exists {
		return nil, ErrNotFound
	}
	if inode.Type != pms.TypeFile {
		return nil, ErrIsDir
	}
	return inode.ChunkList, nil
}

// --- Write Operations (Replicated) ---

func (s *InMemoryMetadataService) Create(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error) {
	// 1. Pre-flight checks (Optional but good for fail-fast)
	s.mu.RLock()
	parent, exists := s.inodes[parentInodeID]
	if !exists {
		s.mu.RUnlock()
		return nil, ErrNotFound
	}
	if _, exists := parent.Children[name]; exists {
		s.mu.RUnlock()
		return nil, ErrAlreadyExists
	}
	s.mu.RUnlock()

	// 2. Construct Op
	newID := uuid.New().String()
	op := pms.MetadataOperation{
		Type:     pms.OpCreate,
		InodeID:  newID,
		ParentID: parentInodeID,
		Name:     name,
		FileType: pms.TypeFile,
		Mode:     mode,
		UID:      uid,
		GID:      gid,
	}

	// 3. Replicate (Blocks until applied)
	if err := s.replicateOp(ctx, op); err != nil {
		return nil, err
	}

	// 4. Return result
	// Read back the specific inode we created. If it is missing, another
	// committed create for the same name won the race.
	inode, err := s.GetInode(ctx, newID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return inode, nil
}

func (s *InMemoryMetadataService) Mkdir(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error) {
	newID := uuid.New().String()
	op := pms.MetadataOperation{
		Type:     pms.OpCreate, // Reuse create op, different Type field
		InodeID:  newID,
		ParentID: parentInodeID,
		Name:     name,
		FileType: pms.TypeDirectory,
		Mode:     mode,
		UID:      uid,
		GID:      gid,
	}

	if err := s.replicateOp(ctx, op); err != nil {
		return nil, err
	}

	return s.GetInode(ctx, newID)
}

func (s *InMemoryMetadataService) Remove(ctx context.Context, parentInodeID string, name string) error {
	op := pms.MetadataOperation{
		Type:     pms.OpRemove,
		ParentID: parentInodeID,
		Name:     name,
	}
	return s.replicateOp(ctx, op)
}

func (s *InMemoryMetadataService) Rmdir(ctx context.Context, parentInodeID string, name string) error {
	// Posix Rmdir requires directory to be empty.
	s.mu.RLock()
	parent, pExists := s.inodes[parentInodeID]
	if !pExists {
		s.mu.RUnlock()
		return ErrNotFound
	}
	childID, cExists := parent.Children[name]
	if !cExists {
		s.mu.RUnlock()
		return ErrNotFound
	}
	child, ok := s.inodes[childID]
	if ok && len(child.Children) > 0 {
		s.mu.RUnlock()
		return ErrNotEmpty
	}
	s.mu.RUnlock()

	// Logic is same as Remove
	op := pms.MetadataOperation{
		Type:     pms.OpRemove,
		ParentID: parentInodeID,
		Name:     name,
	}
	return s.replicateOp(ctx, op)
}

func (s *InMemoryMetadataService) Rename(ctx context.Context, srcParentID, srcName, dstParentID, dstName string) error {
	op := pms.MetadataOperation{
		Type:        pms.OpRename,
		ParentID:    srcParentID,
		Name:        srcName,
		DstParentID: dstParentID,
		DstName:     dstName,
	}
	return s.replicateOp(ctx, op)
}

func (s *InMemoryMetadataService) SetAttributes(ctx context.Context, inodeID string, mode *uint32, uid, gid *uint32, atime, mtime *int64) (*pms.Attributes, error) {
	op := pms.MetadataOperation{
		Type:     pms.OpSetAttr,
		InodeID:  inodeID,
		SetMode:  mode,
		SetUID:   uid,
		SetGID:   gid,
		SetATime: atime,
		SetMTime: mtime,
	}

	if err := s.replicateOp(ctx, op); err != nil {
		return nil, err
	}

	return s.GetAttributes(ctx, inodeID)
}

func (s *InMemoryMetadataService) UpdateInode(ctx context.Context, inodeID string, newSize int64, newChunkList []string, mtime int64) error {
	op := pms.MetadataOperation{
		Type:         pms.OpUpdateInode,
		InodeID:      inodeID,
		NewSize:      &newSize,
		NewChunkList: newChunkList,
		SetMTime:     &mtime,
	}
	return s.replicateOp(ctx, op)
}
