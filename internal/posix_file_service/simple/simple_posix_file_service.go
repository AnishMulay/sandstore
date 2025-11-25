package simple

import (
	"context"
	"time"

	"github.com/AnishMulay/sandstore/internal/log_service"
	pcs "github.com/AnishMulay/sandstore/internal/posix_chunk_service"
	pfsinternal "github.com/AnishMulay/sandstore/internal/posix_file_service/internal"
	pms "github.com/AnishMulay/sandstore/internal/posix_metadata_service"
	"github.com/google/uuid"
)

type SimplePosixFileService struct {
	ms        pms.PosixMetadataService
	cs        pcs.PosixChunkService
	ls        log_service.LogService
	chunkSize int64
}

func NewSimplePosixFileService(
	ms pms.PosixMetadataService,
	cs pcs.PosixChunkService,
	ls log_service.LogService,
) *SimplePosixFileService {
	return &SimplePosixFileService{
		ms: ms,
		cs: cs,
		ls: ls,
	}
}

// --- Lifecycle ---

func (s *SimplePosixFileService) Start() error {
	s.ls.Info(log_service.LogEvent{Message: "Starting Simple POSIX File Service"})

	// 1. Start Metadata Service
	if err := s.ms.Start(); err != nil {
		return err
	}

	// 2. Fetch Config (ChunkSize) from Metadata Service (Superblock)
	// We need a context here, usually passed in Start or we create a background one
	ctx := context.Background()
	info, err := s.ms.GetFsInfo(ctx)
	if err != nil {
		s.ls.Error(log_service.LogEvent{
			Message:  "Failed to fetch FS Info during startup",
			Metadata: map[string]any{"error": err.Error()},
		})
		return err
	}
	s.chunkSize = info.ChunkSize
	s.ls.Info(log_service.LogEvent{
		Message:  "Configured File Service",
		Metadata: map[string]any{"chunkSize": s.chunkSize},
	})

	return nil
}

func (s *SimplePosixFileService) Stop() error {
	s.ls.Info(log_service.LogEvent{Message: "Stopping Simple POSIX File Service"})
	return s.ms.Stop()
}

// --- Data Operations (The Complex Logic) ---

func (s *SimplePosixFileService) Read(ctx context.Context, inodeID string, offset int64, length int64) ([]byte, error) {
	s.ls.Debug(log_service.LogEvent{
		Message:  "Read Request",
		Metadata: map[string]any{"inodeID": inodeID, "offset": offset, "length": length},
	})

	// 1. Get Inode to find chunks
	inode, err := s.ms.GetInode(ctx, inodeID)
	if err != nil {
		return nil, err
	}
	if inode.Type != pms.TypeFile {
		return nil, pfsinternal.ErrIsDirectory
	}

	// 2. Bounds Check
	if offset >= inode.FileSize {
		return []byte{}, nil // EOF
	}
	if offset+length > inode.FileSize {
		length = inode.FileSize - offset
	}

	// 3. Calculate Chunks
	startChunkIdx := offset / s.chunkSize
	endChunkIdx := (offset + length - 1) / s.chunkSize

	result := make([]byte, length)
	resultOffset := 0

	// 4. Iterate and Read
	for i := startChunkIdx; i <= endChunkIdx; i++ {
		// Check for sparse files (missing chunks)
		if int(i) >= len(inode.ChunkList) {
			break // Should not happen if FileSize is correct
		}
		chunkID := inode.ChunkList[i]

		// Calculate fetch logic
		chunkPos := i * s.chunkSize
		chunkReadStart := int64(0)
		if i == startChunkIdx {
			chunkReadStart = offset - chunkPos
		}

		chunkReadEnd := s.chunkSize
		if i == endChunkIdx {
			chunkReadEnd = (offset + length) - chunkPos
		}

		// Fetch Chunk
		data, err := s.cs.ReadChunk(chunkID)
		if err != nil {
			s.ls.Error(log_service.LogEvent{
				Message:  "Failed to read chunk",
				Metadata: map[string]any{"chunkID": chunkID, "error": err.Error()},
			})
			return nil, pfsinternal.ErrChunkActionFailed
		}

		// Copy relevant slice to result buffer
		// Safety check for bounds
		if chunkReadStart < int64(len(data)) {
			end := chunkReadEnd
			if end > int64(len(data)) {
				end = int64(len(data))
			}
			n := copy(result[resultOffset:], data[chunkReadStart:end])
			resultOffset += n
		}
	}

	return result, nil
}

func (s *SimplePosixFileService) Write(ctx context.Context, inodeID string, offset int64, data []byte) (int64, error) {
	s.ls.Debug(log_service.LogEvent{
		Message:  "Write Request",
		Metadata: map[string]any{"inodeID": inodeID, "offset": offset, "len": len(data)},
	})

	// 1. Get Current State
	inode, err := s.ms.GetInode(ctx, inodeID)
	if err != nil {
		return 0, err
	}
	if inode.Type != pms.TypeFile {
		return 0, pfsinternal.ErrIsDirectory
	}

	// 2. Expand ChunkList if needed
	// Logic: If we write past the current last chunk, we need new chunk IDs.
	endPos := offset + int64(len(data))
	maxChunkIdx := (endPos - 1) / s.chunkSize

	// Clone the list to modify
	newChunkList := make([]string, len(inode.ChunkList))
	copy(newChunkList, inode.ChunkList)

	for int64(len(newChunkList)) <= maxChunkIdx {
		newID := uuid.New().String()
		newChunkList = append(newChunkList, newID)
	}

	// 3. Perform Writes
	startChunkIdx := offset / s.chunkSize
	endChunkIdx := maxChunkIdx
	dataOffset := 0

	for i := startChunkIdx; i <= endChunkIdx; i++ {
		chunkID := newChunkList[i]

		// Calculate boundaries relative to this chunk
		chunkStartPos := i * s.chunkSize

		writeStartInChunk := int64(0)
		if i == startChunkIdx {
			writeStartInChunk = offset - chunkStartPos
		}

		writeEndInChunk := s.chunkSize
		if i == endChunkIdx {
			writeEndInChunk = endPos - chunkStartPos
		}

		bytesToWrite := writeEndInChunk - writeStartInChunk
		chunkDataToWrite := data[dataOffset : dataOffset+int(bytesToWrite)]

		// READ-MODIFY-WRITE Pattern
		// We need to read the existing chunk if we are doing a partial overwrite
		// or if the chunk already exists and we aren't overwriting the whole thing.
		var finalChunkData []byte

		// Check if we are overwriting the entire 8MB chunk (Optimization: skip read)
		isFullOverwrite := writeStartInChunk == 0 && writeEndInChunk == s.chunkSize
		// Also check if this is a brand new chunk (nothing to read)
		isNewChunk := int(i) >= len(inode.ChunkList)

		if isFullOverwrite || (isNewChunk && writeStartInChunk == 0) {
			finalChunkData = chunkDataToWrite
			// Pad with zeros if it's a new chunk but we aren't filling it to 8MB?
			// Usually ChunkService just stores what we give.
			// But for random access, we prefer fixed size blocks or handle partials.
			// Let's assume we just write the bytes we have for the tail.
		} else {
			// We need to read previous data
			var existingData []byte
			if !isNewChunk {
				existingData, err = s.cs.ReadChunk(chunkID)
				if err != nil {
					// If read fails, we might assume it's empty/lost?
					// Strict consistency says fail.
					return 0, pfsinternal.ErrChunkActionFailed
				}
			}

			// Create a buffer of ChunkSize (or enough to hold existing + new)
			// Standard logic: expand existing data to ChunkSize if needed
			bufferSize := s.chunkSize
			if int64(len(existingData)) > bufferSize {
				bufferSize = int64(len(existingData))
			}
			// If this is the last chunk, it might be smaller

			finalChunkData = make([]byte, bufferSize)
			copy(finalChunkData, existingData) // Copy old

			// Overlay new
			copy(finalChunkData[writeStartInChunk:], chunkDataToWrite)

			// Trim to actual size if it's the last chunk?
			// Usually chunks 0..N-1 are full size. Chunk N is partial.
			if i == endChunkIdx {
				finalSize := writeEndInChunk
				if int64(len(existingData)) > finalSize {
					finalSize = int64(len(existingData))
				}
				finalChunkData = finalChunkData[:finalSize]
			} else {
				finalChunkData = finalChunkData[:s.chunkSize]
			}
		}

		// Write to Data Plane
		if err := s.cs.WriteChunk(chunkID, finalChunkData); err != nil {
			return 0, pfsinternal.ErrChunkActionFailed
		}

		dataOffset += int(bytesToWrite)
	}

	// 4. Update Metadata (Control Plane)
	newFileSize := inode.FileSize
	if endPos > newFileSize {
		newFileSize = endPos
	}

	err = s.ms.UpdateInode(ctx, inodeID, newFileSize, newChunkList, time.Now().UnixNano())
	if err != nil {
		// Orphan chunk risk here (Learning Point!)
		return 0, pfsinternal.ErrMetadataActionFailed
	}

	return int64(len(data)), nil
}

// --- Directory & Metadata Operations (Delegation) ---

func (s *SimplePosixFileService) Create(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error) {
	return s.ms.Create(ctx, parentInodeID, name, mode, uid, gid)
}

func (s *SimplePosixFileService) Mkdir(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error) {
	return s.ms.Mkdir(ctx, parentInodeID, name, mode, uid, gid)
}

func (s *SimplePosixFileService) Remove(ctx context.Context, parentInodeID string, name string) error {
	// 1. Resolve ID to clean up chunks later
	inodeID, err := s.ms.Lookup(ctx, parentInodeID, name)
	if err != nil {
		return err
	}

	inode, err := s.ms.GetInode(ctx, inodeID)
	if err != nil {
		return err
	}

	// 2. Unlink from Metadata
	if err := s.ms.Remove(ctx, parentInodeID, name); err != nil {
		return err
	}

	// 3. Garbage Collection (Simple/Sync)
	// Only delete chunks if it was a file
	if inode.Type == pms.TypeFile {
		for _, chunkID := range inode.ChunkList {
			// Best effort delete
			if err := s.cs.DeleteChunk(chunkID); err != nil {
				s.ls.Warn(log_service.LogEvent{
					Message:  "Failed to GC chunk after remove",
					Metadata: map[string]any{"chunkID": chunkID},
				})
			}
		}
	}

	return nil
}

func (s *SimplePosixFileService) Rmdir(ctx context.Context, parentInodeID string, name string) error {
	return s.ms.Rmdir(ctx, parentInodeID, name)
}

func (s *SimplePosixFileService) Rename(ctx context.Context, srcParentID, srcName, dstParentID, dstName string) error {
	return s.ms.Rename(ctx, srcParentID, srcName, dstParentID, dstName)
}

func (s *SimplePosixFileService) GetAttr(ctx context.Context, inodeID string) (*pms.Attributes, error) {
	return s.ms.GetAttributes(ctx, inodeID)
}

func (s *SimplePosixFileService) SetAttr(ctx context.Context, inodeID string, mode *uint32, uid, gid *uint32, atime, mtime *int64) (*pms.Attributes, error) {
	return s.ms.SetAttributes(ctx, inodeID, mode, uid, gid, atime, mtime)
}

func (s *SimplePosixFileService) Lookup(ctx context.Context, parentInodeID string, name string) (string, error) {
	return s.ms.Lookup(ctx, parentInodeID, name)
}

func (s *SimplePosixFileService) LookupPath(ctx context.Context, path string) (string, error) {
	return s.ms.LookupPath(ctx, path)
}

func (s *SimplePosixFileService) Access(ctx context.Context, inodeID string, uid, gid uint32, accessMask uint32) error {
	return s.ms.Access(ctx, inodeID, uid, gid, accessMask)
}

func (s *SimplePosixFileService) ReadDir(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntry, int, bool, error) {
	return s.ms.ReadDir(ctx, inodeID, cookie, maxEntries)
}

func (s *SimplePosixFileService) ReadDirPlus(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntryPlus, int, bool, error) {
	return s.ms.ReadDirPlus(ctx, inodeID, cookie, maxEntries)
}

func (s *SimplePosixFileService) GetFsStat(ctx context.Context) (*pms.FileSystemStats, error) {
	return s.ms.GetFsStat(ctx)
}

func (s *SimplePosixFileService) GetFsInfo(ctx context.Context) (*pms.FileSystemInfo, error) {
	return s.ms.GetFsInfo(ctx)
}
