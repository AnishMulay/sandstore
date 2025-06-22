package file_service

import (
	"bytes"
	"testing"

	"github.com/AnishMulay/sandstore/internal/chunk_service"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
)

func TestDefaultFileService_StoreFile(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		data      []byte
		chunkSize int64
		wantErr   bool
	}{
		{
			name:      "store small file",
			path:      "/test/small.txt",
			data:      []byte("hello world"),
			chunkSize: 1024,
			wantErr:   false,
		},
		{
			name:      "store file with multiple chunks",
			path:      "/test/large.txt",
			data:      []byte("this is a test file that will be split into multiple chunks"),
			chunkSize: 10,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			ms := metadata_service.NewInMemoryMetadataService()
			cs := chunk_service.NewLocalDiscChunkService(tempDir)
			fs := NewDefaultFileService(ms, cs)
			fs.chunkSize = tt.chunkSize

			err := fs.StoreFile(tt.path, tt.data)

			if (err != nil) != tt.wantErr {
				t.Errorf("StoreFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				metadata, err := ms.GetFileMetadata(tt.path)
				if err != nil {
					t.Errorf("StoreFile() failed to create metadata: %v", err)
					return
				}

				if metadata.Size != int64(len(tt.data)) {
					t.Errorf("StoreFile() metadata size = %v, want %v", metadata.Size, len(tt.data))
				}

				expectedChunks := (len(tt.data) + int(tt.chunkSize) - 1) / int(tt.chunkSize)
				if len(tt.data) == 0 {
					expectedChunks = 1
				}
				if len(metadata.Chunks) != expectedChunks {
					t.Errorf("StoreFile() chunk count = %v, want %v", len(metadata.Chunks), expectedChunks)
				}
			}
		})
	}
}

func TestDefaultFileService_ReadFile(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		data      []byte
		chunkSize int64
		setupFn   func(*DefaultFileService)
		wantErr   bool
	}{
		{
			name:      "read existing file",
			path:      "/test/file.txt",
			data:      []byte("hello world"),
			chunkSize: 1024,
			setupFn: func(fs *DefaultFileService) {
				_ = fs.StoreFile("/test/file.txt", []byte("hello world"))
			},
			wantErr: false,
		},
		{
			name:      "read non-existent file",
			path:      "/test/missing.txt",
			data:      nil,
			chunkSize: 1024,
			setupFn:   nil,
			wantErr:   true,
		},
		{
			name:      "read file with multiple chunks",
			path:      "/test/large.txt",
			data:      []byte("this is a test file that will be split into multiple chunks"),
			chunkSize: 10,
			setupFn: func(fs *DefaultFileService) {
				_ = fs.StoreFile("/test/large.txt", []byte("this is a test file that will be split into multiple chunks"))
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			ms := metadata_service.NewInMemoryMetadataService()
			cs := chunk_service.NewLocalDiscChunkService(tempDir)
			fs := NewDefaultFileService(ms, cs)
			fs.chunkSize = tt.chunkSize

			if tt.setupFn != nil {
				tt.setupFn(fs)
			}

			data, err := fs.ReadFile(tt.path)

			if (err != nil) != tt.wantErr {
				t.Errorf("ReadFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if !bytes.Equal(data, tt.data) {
					t.Errorf("ReadFile() data = %v, want %v", data, tt.data)
				}
			}
		})
	}
}

func TestDefaultFileService_DeleteFile(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		data      []byte
		chunkSize int64
		setupFn   func(*DefaultFileService)
		wantErr   bool
	}{
		{
			name:      "delete existing file",
			path:      "/test/file.txt",
			data:      []byte("hello world"),
			chunkSize: 1024,
			setupFn: func(fs *DefaultFileService) {
				_ = fs.StoreFile("/test/file.txt", []byte("hello world"))
			},
			wantErr: false,
		},
		{
			name:      "delete non-existent file",
			path:      "/test/missing.txt",
			data:      nil,
			chunkSize: 1024,
			setupFn:   nil,
			wantErr:   true,
		},
		{
			name:      "delete file with multiple chunks",
			path:      "/test/large.txt",
			data:      []byte("this is a test file that will be split into multiple chunks"),
			chunkSize: 10,
			setupFn: func(fs *DefaultFileService) {
				_ = fs.StoreFile("/test/large.txt", []byte("this is a test file that will be split into multiple chunks"))
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			ms := metadata_service.NewInMemoryMetadataService()
			cs := chunk_service.NewLocalDiscChunkService(tempDir)
			fs := NewDefaultFileService(ms, cs)
			fs.chunkSize = tt.chunkSize

			if tt.setupFn != nil {
				tt.setupFn(fs)
			}

			err := fs.DeleteFile(tt.path)

			if (err != nil) != tt.wantErr {
				t.Errorf("DeleteFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				_, err := fs.ReadFile(tt.path)
				if err == nil {
					t.Errorf("DeleteFile() file still readable after deletion")
				}
			}
		})
	}
}
