package file_service

import (
	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
)

type DefaultFileService struct {
	ms metadata_service.MetadataService
	cs chunk_service.ChunkService
}

func NewDefaultFileService(ms metadata_service.MetadataService, cs chunk_service.ChunkService) *DefaultFileService {
	return &DefaultFileService{
		ms: ms,
		cs: cs,
	}
}
