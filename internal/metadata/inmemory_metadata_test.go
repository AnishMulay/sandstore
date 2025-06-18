package metadata

import (
	"testing"
)

func TestInMemoryMetadataService_CreateFile(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		size    int64
		wantErr bool
		errorIs error
		setupFn func(*InMemoryMetadataService)
	}{
		{
			name:    "create new file",
			path:    "/test/file.txt",
			size:    100,
			wantErr: false,
		},
		{
			name:    "create file with zero size",
			path:    "/test/empty.txt",
			size:    0,
			wantErr: false,
		},
		{
			name:    "create file with duplicate path",
			path:    "/test/duplicate.txt",
			size:    100,
			wantErr: true,
			errorIs: ErrFileAlreadyExists,
			setupFn: func(ms *InMemoryMetadataService) {
				_ = ms.CreateFileMetadata("/test/duplicate.txt", 50)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := NewInMemoryMetadataService()

			if tt.setupFn != nil {
				tt.setupFn(ms)
			}

			err := ms.CreateFileMetadata(tt.path, tt.size)

			if (err != nil) != tt.wantErr {
				t.Errorf("CreateFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.errorIs != nil && err != tt.errorIs {
				t.Errorf("CreateFile() error = %v, want %v", err, tt.errorIs)
				return
			}

			if !tt.wantErr {
				file, exists := ms.files[tt.path]
				if !exists {
					t.Errorf("CreateFile() file not found in map")
					return
				}

				if file.Path != tt.path {
					t.Errorf("CreateFile() file.Path = %v, want %v", file.Path, tt.path)
				}

				if file.Size != tt.size {
					t.Errorf("CreateFile() file.Size = %v, want %v", file.Size, tt.size)
				}

				if file.CreatedAt.IsZero() {
					t.Errorf("CreateFile() file.CreatedAt is zero")
				}

				if file.ModifiedAt.IsZero() {
					t.Errorf("CreateFile() file.ModifiedAt is zero")
				}

				if file.Permissions != "rw-r--r--" {
					t.Errorf("CreateFile() file.Permissions = %v, want %v", file.Permissions, "rw-r--r--")
				}
			}
		})
	}
}
