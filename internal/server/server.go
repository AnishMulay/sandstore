package server

import "github.com/AnishMulay/sandstore/internal/communication"

type Server interface {
	Start() error
	Stop() error
	RegisterHandler(msgType string, handler func(msg communication.Message) (*communication.Response, error))
}
