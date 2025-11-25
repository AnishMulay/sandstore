package posix_server

type PosixServer interface {
	Start() error
	Stop() error
}