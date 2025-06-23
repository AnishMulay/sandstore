package server

type Server interface {
	Start() error
	Stop() error
}
