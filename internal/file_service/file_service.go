package file_service

type FileService interface {
	StoreFile(path string, data []byte) error
	ReadFile(path string) ([]byte, error)
	DeleteFile(path string) error
}
