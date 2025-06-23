package server

import (
	"context"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service"
)

type DefaultServer struct {
	comm   communication.Communicator
	fs     file_service.FileService
	ctx    context.Context
	cancel context.CancelFunc
}
