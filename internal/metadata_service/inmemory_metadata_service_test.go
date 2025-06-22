package metadata_service

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
				_ = ms.CreateFileMetadata("/test/duplicate.txt", 50, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := NewInMemoryMetadataService()

			if tt.setupFn != nil {
				tt.setupFn(ms)
			}

			err := ms.CreateFileMetadata(tt.path, tt.size, nil)

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

func TestInMemoryMetadataService_GetFileMetadata(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		setupFn func(*InMemoryMetadataService)
		wantErr error
	}{
		{
			name: "get existing file",
			path: "/test/file.txt",
			setupFn: func(ms *InMemoryMetadataService) {
				_ = ms.CreateFileMetadata("/test/file.txt", 100, nil)
			},
			wantErr: nil,
		},
		{
			name:    "get non-existent file",
			path:    "/test/missing.txt",
			setupFn: nil,
			wantErr: ErrFileNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := NewInMemoryMetadataService()

			if tt.setupFn != nil {
				tt.setupFn(ms)
			}

			file, err := ms.GetFileMetadata(tt.path)

			if err != tt.wantErr {
				t.Errorf("GetFileMetadata() error = %v, want %v", err, tt.wantErr)
				return
			}

			if tt.wantErr == nil {
				if file == nil {
					t.Errorf("GetFileMetadata() returned nil file with no error")
					return
				}

				if file.Path != tt.path {
					t.Errorf("GetFileMetadata() file.Path = %v, want %v", file.Path, tt.path)
				}

				if file.Size != 100 {
					t.Errorf("GetFileMetadata() file.Size = %v, want %v", file.Size, 100)
				}

				if file.Permissions != "rw-r--r--" {
					t.Errorf("GetFileMetadata() file.Permissions = %v, want %v", file.Permissions, "rw-r--r--")
				}
			} else {
				if file != nil {
					t.Errorf("GetFileMetadata() expected nil file with error, got %v", file)
				}
			}
		})
	}
}

func TestInMemoryMetadataService_DeleteFileMetadata(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		setupFn func(*InMemoryMetadataService)
		wantErr error
	}{
		{
			name: "delete existing file",
			path: "/test/file.txt",
			setupFn: func(ms *InMemoryMetadataService) {
				_ = ms.CreateFileMetadata("/test/file.txt", 100, nil)
			},
			wantErr: nil,
		},
		{
			name:    "delete non-existent file",
			path:    "/test/missing.txt",
			setupFn: nil,
			wantErr: ErrFileNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := NewInMemoryMetadataService()

			if tt.setupFn != nil {
				tt.setupFn(ms)
			}

			err := ms.DeleteFileMetadata(tt.path)

			if err != tt.wantErr {
				t.Errorf("DeleteFileMetadata() error = %v, want %v", err, tt.wantErr)
				return
			}

			if tt.wantErr == nil {
				_, err := ms.GetFileMetadata(tt.path)
				if err != ErrFileNotFound {
					t.Errorf("File still exists after deletion, got error = %v, want %v", err, ErrFileNotFound)
				}
			}
		})
	}
}

func TestInMemoryMetadataService_ListDirectory(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		setupFn   func(*InMemoryMetadataService)
		wantCount int
	}{
		{
			name: "list files in directory with files",
			path: "/test",
			setupFn: func(ms *InMemoryMetadataService) {
				_ = ms.CreateFileMetadata("/test/file1.txt", 100, nil)
				_ = ms.CreateFileMetadata("/test/file2.txt", 200, nil)
				_ = ms.CreateFileMetadata("/other/file3.txt", 300, nil)
			},
			wantCount: 2,
		},
		{
			name: "list files in empty directory",
			path: "/empty",
			setupFn: func(ms *InMemoryMetadataService) {
				_ = ms.CreateFileMetadata("/test/file1.txt", 100, nil)
			},
			wantCount: 0,
		},
		{
			name: "list files in nested directory",
			path: "/test/sub",
			setupFn: func(ms *InMemoryMetadataService) {
				_ = ms.CreateFileMetadata("/test/file1.txt", 100, nil)
				_ = ms.CreateFileMetadata("/test/sub/file2.txt", 200, nil)
				_ = ms.CreateFileMetadata("/test/sub/nested/file3.txt", 300, nil)
			},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ms := NewInMemoryMetadataService()

			if tt.setupFn != nil {
				tt.setupFn(ms)
			}

			files, err := ms.ListDirectory(tt.path)

			if err != nil {
				t.Errorf("ListDirectory() error = %v, want nil", err)
				return
			}

			if len(files) != tt.wantCount {
				t.Errorf("ListDirectory() returned %d files, want %d", len(files), tt.wantCount)
			}
		})
	}
}