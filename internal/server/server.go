package server

import (
	"reflect"
	"github.com/AnishMulay/sandstore/internal/communication"
)

// TypedHandler stores a handler function with its expected payload type
type TypedHandler struct {
	Handler     func(msg communication.Message) (*communication.Response, error)
	PayloadType reflect.Type
}

type Server interface {
	Start() error
	Stop() error
	RegisterHandler(msgType string, handler func(msg communication.Message) (*communication.Response, error))
	RegisterTypedHandler(msgType string, payloadType reflect.Type, handler func(msg communication.Message) (*communication.Response, error))
}
