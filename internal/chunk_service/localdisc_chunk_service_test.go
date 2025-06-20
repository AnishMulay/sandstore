package chunk_service

import (
	"bytes"
	"os"
	"testing"
)

func TestLocalDiscChunkService_WriteChunk(t *testing.T) {
	tests := []struct {
		name    string
		chunkID string
		data    []byte
		wantErr bool
	}{
		{
			name:    "write chunk with data",
			chunkID: "test-chunk-1",
			data:    []byte("hello world"),
			wantErr: false,
		},
		{
			name:    "write empty chunk",
			chunkID: "empty-chunk",
			data:    []byte{},
			wantErr: false,
		},
		{
			name:    "write binary data",
			chunkID: "binary-chunk",
			data:    []byte{0x00, 0x01, 0x02, 0xFF},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			cs := NewLocalDiscChunkService(tempDir)

			err := cs.WriteChunk(tt.chunkID, tt.data)

			if (err != nil) != tt.wantErr {
				t.Errorf("WriteChunk() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				path := cs.chunkPath(tt.chunkID)
				if _, err := os.Stat(path); os.IsNotExist(err) {
					t.Errorf("WriteChunk() file not created at %s", path)
					return
				}

				writtenData, err := os.ReadFile(path)
				if err != nil {
					t.Errorf("WriteChunk() failed to read written file: %v", err)
					return
				}

				if !bytes.Equal(writtenData, tt.data) {
					t.Errorf("WriteChunk() written data = %v, want %v", writtenData, tt.data)
				}
			}
		})
	}
}

func TestLocalDiscChunkService_ReadChunk(t *testing.T) {
	tests := []struct {
		name    string
		chunkID string
		data    []byte
		setupFn func(*LocalDiscChunkService)
		wantErr bool
	}{
		{
			name:    "read existing chunk",
			chunkID: "test-chunk-1",
			data:    []byte("hello world"),
			setupFn: func(cs *LocalDiscChunkService) {
				_ = cs.WriteChunk("test-chunk-1", []byte("hello world"))
			},
			wantErr: false,
		},
		{
			name:    "read non-existent chunk",
			chunkID: "missing-chunk",
			data:    nil,
			setupFn: nil,
			wantErr: true,
		},
		{
			name:    "read empty chunk",
			chunkID: "empty-chunk",
			data:    []byte{},
			setupFn: func(cs *LocalDiscChunkService) {
				_ = cs.WriteChunk("empty-chunk", []byte{})
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			cs := NewLocalDiscChunkService(tempDir)

			if tt.setupFn != nil {
				tt.setupFn(cs)
			}

			data, err := cs.ReadChunk(tt.chunkID)

			if (err != nil) != tt.wantErr {
				t.Errorf("ReadChunk() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if !bytes.Equal(data, tt.data) {
					t.Errorf("ReadChunk() data = %v, want %v", data, tt.data)
				}
			}
		})
	}
}

func TestLocalDiscChunkService_DeleteChunk(t *testing.T) {
	tests := []struct {
		name    string
		chunkID string
		setupFn func(*LocalDiscChunkService)
		wantErr bool
	}{
		{
			name:    "delete existing chunk",
			chunkID: "test-chunk-1",
			setupFn: func(cs *LocalDiscChunkService) {
				_ = cs.WriteChunk("test-chunk-1", []byte("test data"))
			},
			wantErr: false,
		},
		{
			name:    "delete non-existent chunk",
			chunkID: "missing-chunk",
			setupFn: nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			cs := NewLocalDiscChunkService(tempDir)

			if tt.setupFn != nil {
				tt.setupFn(cs)
			}

			err := cs.DeleteChunk(tt.chunkID)

			if (err != nil) != tt.wantErr {
				t.Errorf("DeleteChunk() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				path := cs.chunkPath(tt.chunkID)
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("DeleteChunk() file still exists at %s", path)
				}
			}
		})
	}
}
