package simple

import (
	"os"
	"os/signal"
	"reflect"
	"syscall"

	chunkservice "github.com/AnishMulay/sandstore/internal/chunk_service/localdisc"
	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/file_service/defaultfs"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/AnishMulay/sandstore/internal/metadata_service"
	"github.com/AnishMulay/sandstore/internal/server"
)

type Options struct {
	NodeID     string
	ListenAddr string
	DataDir    string
}

type runnable interface {
	Run() error
}

type singleNodeServer struct {
	server *server.DefaultServer
}

func (s *singleNodeServer) Run() error {
	if err := s.server.Start(); err != nil {
		return err
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	return s.server.Stop()
}

func Build(opts Options) runnable {
	logDir := opts.DataDir + "/logs"
	chunkDir := opts.DataDir + "/chunks/" + opts.NodeID

	ls := log_service.NewLocalDiscLogService(logDir, opts.NodeID, "INFO")
	ms := metadata_service.NewInMemoryMetadataService(ls)
	cs := chunkservice.NewLocalDiscChunkService(chunkDir, ls)
	comm := communication.NewGRPCCommunicator(opts.ListenAddr, ls)
	clusterService := cluster_service.NewInMemoryClusterService([]cluster_service.Node{}, ls)
	fs := defaultfs.NewDefaultFileService(ms, cs, ls, 8*1024*1024)

	srv := server.NewDefaultServer(comm, fs, ls, clusterService)

	srv.RegisterTypedHandler(communication.MessageTypeStoreFile, reflect.TypeOf((*communication.StoreFileRequest)(nil)).Elem(), srv.HandleStoreFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeReadFile, reflect.TypeOf((*communication.ReadFileRequest)(nil)).Elem(), srv.HandleReadFileMessage)
	srv.RegisterTypedHandler(communication.MessageTypeDeleteFile, reflect.TypeOf((*communication.DeleteFileRequest)(nil)).Elem(), srv.HandleDeleteFileMessage)

	return &singleNodeServer{server: srv}
}
