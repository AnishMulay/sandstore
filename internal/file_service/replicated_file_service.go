package file_service

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/AnishMulay/sandstore/internal/chunk_replicator"
	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/metadata_replicator"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/google/uuid"
)

type ReplicatedFileService struct {
	ms        metadata_service.MetadataService
	cs        chunk_service.ChunkService
	cr        chunk_replicator.ChunkReplicator
	mr        metadata_replicator.MetadataReplicator
	chunkSize int64
}

func NewReplicatedFileService(ms metadata_service.MetadataService, cs chunk_service.ChunkService, cr chunk_replicator.ChunkReplicator, mr metadata_replicator.MetadataReplicator, chunkSize int64) *ReplicatedFileService {
	return &ReplicatedFileService{
		ms:        ms,
		cs:        cs,
		cr:        cr,
		mr:        mr,
		chunkSize: chunkSize,
	}
}

func (fs *ReplicatedFileService) StoreFile(path string, data []byte) error {
	var chunks []chunk_service.FileChunk
	offset := 0
	now := time.Now()
	fileID := uuid.New().String()

	counter := 0

	for offset < len(data) {
		end := offset + int(fs.chunkSize)

		if end > len(data) {
			end = len(data)
		}

		chunkData := data[offset:end]
		chunkID := uuid.New().String()
		checksumRaw := sha256.Sum256(chunkData)
		checksum := fmt.Sprintf("%x", checksumRaw)

		err := fs.cs.WriteChunk(chunkID, chunkData)
		if err != nil {
			return fmt.Errorf("failed to store chunk: %w", err)
		}

		replicas, err := fs.cr.ReplicateChunk(chunkID, chunkData, 2) // Assuming replication factor of 3

		chunks = append(chunks, chunk_service.FileChunk{
			ChunkID:    chunkID,
			FileID:     fileID,
			Size:       int64(len(chunkData)),
			CreatedAt:  now,
			ModifiedAt: now,
			Checksum:   checksum,
			Replicas:   replicas,
		})

		counter++
		offset = end
		if counter >= 5 {
			select {}
		}
	}

	err := fs.ms.CreateFileMetadata(path, int64(len(data)), chunks)

	// return fs.ms.CreateFileMetadata(path, int64(len(data)), chunks)

	if err != nil {
		return fmt.Errorf("failed to create file metadata: %w", err)
	}

	metadata := &metadata_service.FileMetadata{
		Path:       path,
		Size:       int64(len(data)),
		Chunks:     chunks,
		CreatedAt:  now,
		ModifiedAt: now,
	}

	err = fs.mr.ReplicateMetadata(*metadata)
	if err != nil {
		return fmt.Errorf("failed to replicate metadata: %w", err)
	}

	return nil
}

func (fs *ReplicatedFileService) ReadFile(path string) ([]byte, error) {
	metadata, err := fs.ms.GetFileMetadata(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get file metadata: %w", err)
	}

	var data []byte
	for _, chunk := range metadata.Chunks {
		chunkData, err := fs.cs.ReadChunk(chunk.ChunkID)
		if err != nil {
			return nil, fmt.Errorf("failed to read chunk: %w", err)
		}

		data = append(data, chunkData...)
	}

	return data, nil
}

func (fs *ReplicatedFileService) DeleteFile(path string) error {
	metadata, err := fs.ms.GetFileMetadata(path)
	if err != nil {
		return fmt.Errorf("failed to get file metadata: %w", err)
	}

	for _, chunk := range metadata.Chunks {
		err = fs.cr.DeleteReplicatedChunk(chunk.ChunkID, chunk.Replicas)
		if err != nil {
			return fmt.Errorf("failed to delete replicated chunk: %w", err)
		}

		err = fs.cs.DeleteChunk(chunk.ChunkID)
		if err != nil {
			return fmt.Errorf("failed to delete chunk: %w", err)
		}
	}

	return fs.ms.DeleteFileMetadata(path)
}
