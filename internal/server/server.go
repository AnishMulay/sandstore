package server

import (
	"context"
	"sync"

	"github.com/AnishMulay/sandstore/internal/communication"
)

type Server struct {
	communicator communication.Communicator
	handlers     map[string]communication.MessageHandler
	handlersLock sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}
