package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"gopkg.in/yaml.v3"
)

type MCPConfig struct {
	Communicator struct {
		Type string `yaml:"type"`
	}
	Servers []struct {
		ID      string `yaml:"id"`
		Address string `yaml:"address"`
		Healthy bool   `yaml:"healthy"`
	} `yaml:"servers"`
	DefaultServer string `yaml:"default_server"`
}

type ServerRegistry struct {
	Servers       map[string]string
	DefaultServer string
	Communicator  communication.Communicator
	LogServer     log_service.LogService
}

func LoadConfig(path string) (*MCPConfig, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		defaultConfig := &MCPConfig{}
		defaultConfig.Communicator.Type = "grpc"
		defaultConfig.DefaultServer = "server1"
		defaultConfig.Servers = []struct {
			ID      string `yaml:"id"`
			Address string `yaml:"address"`
			Healthy bool   `yaml:"healthy"`
		}{
			{ID: "server1", Address: "localhost:8080", Healthy: true},
			{ID: "server2", Address: "localhost:8081", Healthy: true},
			{ID: "server3", Address: "localhost:8082", Healthy: true},
		}

		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("Failed to create directory: %v", err)
		}

		data, err := yaml.Marshal(defaultConfig)
		if err != nil {
			return nil, fmt.Errorf("Failed to marshal default config: %v", err)
		}

		if err := os.WriteFile(path, data, 0644); err != nil {
			return nil, fmt.Errorf("Failed to write default config: %v", err)
		}

		return defaultConfig, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read config file: %v", err)
	}

	config := MCPConfig{}
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("Failed to unmarshal config: %v", err)
	}

	return &config, nil
}

func addTools(s *server.MCPServer, registry *ServerRegistry) {
	listServersTool := mcp.NewTool("list_servers",
		mcp.WithDescription("List all available servers"),
	)
	s.AddTool(listServersTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result := "Available servers:\n"
		for id, addr := range registry.Servers {
			result += fmt.Sprintf("- %s: %s\n", id, addr)
		}
		result += fmt.Sprintf("Default server: %s\n", registry.DefaultServer)
		return mcp.NewToolResultText(result), nil
	})

	storeFileTool := mcp.NewTool("store_file",
		mcp.WithDescription("Store a file in the sandstore system"),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Path where the file will be stored"),
		),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("Content of the file to store"),
		),
		mcp.WithString("server",
			mcp.Description("Server ID to use (defaults to the default server)"),
		),
	)
	s.AddTool(storeFileTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleStoreFile(ctx, request, registry)
	})

	readFileTool := mcp.NewTool("read_file",
		mcp.WithDescription("Read a file from the sandstore system"),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Path of the file to read"),
		),
		mcp.WithString("server",
			mcp.Description("Server ID to use (defaults to the default server)"),
		),
	)
	s.AddTool(readFileTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleReadFile(ctx, request, registry)
	})

	deleteFileTool := mcp.NewTool("delete_file",
		mcp.WithDescription("Delete a file from the sandstore system"),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Path of the file to delete"),
		),
		mcp.WithString("server",
			mcp.Description("Server ID to use (defaults to the default server)"),
		),
	)
	s.AddTool(deleteFileTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleDeleteFile(ctx, request, registry)
	})
}

func handleStoreFile(ctx context.Context, request mcp.CallToolRequest, registry *ServerRegistry) (*mcp.CallToolResult, error) {
	path, err := request.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	content, err := request.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	serverID, _ := request.RequireString("server")
	if serverID == "" {
		serverID = registry.DefaultServer
	}

	serverAddr, ok := registry.Servers[serverID]
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("Server %s not found", serverID)), nil
	}

	storeRequest := communication.StoreFileRequest{
		Path: path,
		Data: []byte(content),
	}

	msg := communication.Message{
		From:    "mcp-server",
		Type:    communication.MessageTypeStoreFile,
		Payload: storeRequest,
	}

	resp, err := registry.Communicator.Send(ctx, serverAddr, msg)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to send request: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("File stored successfully, response code: %s", resp.Code)), nil
}

func handleReadFile(ctx context.Context, request mcp.CallToolRequest, registry *ServerRegistry) (*mcp.CallToolResult, error) {
	path, err := request.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	serverID, _ := request.RequireString("server")
	if serverID == "" {
		serverID = registry.DefaultServer
	}

	serverAddr, ok := registry.Servers[serverID]
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("Server %s not found", serverID)), nil
	}

	readRequest := communication.ReadFileRequest{
		Path: path,
	}

	msg := communication.Message{
		From:    "mcp-server",
		Type:    communication.MessageTypeReadFile,
		Payload: readRequest,
	}

	resp, err := registry.Communicator.Send(ctx, serverAddr, msg)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to send request: %v", err)), nil
	}

	if resp.Code == communication.CodeOK {
		return mcp.NewToolResultText(fmt.Sprintf("File content: %s", string(resp.Body))), nil
	} else {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to read file: %s", resp.Code)), nil
	}
}

func handleDeleteFile(ctx context.Context, request mcp.CallToolRequest, registry *ServerRegistry) (*mcp.CallToolResult, error) {
	path, err := request.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	serverID, _ := request.RequireString("server")
	if serverID == "" {
		serverID = registry.DefaultServer
	}

	serverAddr, ok := registry.Servers[serverID]
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("Server %s not found", serverID)), nil
	}

	deleteRequest := communication.DeleteFileRequest{
		Path: path,
	}

	msg := communication.Message{
		From:    "mcp-server",
		Type:    communication.MessageTypeDeleteFile,
		Payload: deleteRequest,
	}

	resp, err := registry.Communicator.Send(ctx, serverAddr, msg)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to send request: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("File deleted successfully, response code: %s", resp.Code)), nil
}

func main() {
	logDir := "./logs"
	nodeID := "mcp-server"
	ls := log_service.NewLocalDiscLogService(logDir, nodeID)

	config, err := LoadConfig("config.yaml")
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		return
	}

	registry := &ServerRegistry{
		Servers:       make(map[string]string),
		DefaultServer: config.DefaultServer,
		LogServer:     ls,
	}

	for _, server := range config.Servers {
		registry.Servers[server.ID] = server.Address
	}

	var comm communication.Communicator
	switch config.Communicator.Type {
	case "grpc":
		comm = communication.NewGRPCCommunicator("mcp-server:9000", ls)
	case "http":
		comm = communication.NewHTTPCommunicator("mcp-server:9000", ls)
	default:
		fmt.Printf("Unknown communicator type: %s\n", config.Communicator.Type)
	}

	registry.Communicator = comm

	s := server.NewMCPServer(
		"SandStore MCP Server",
		"1.0.0",
		server.WithToolCapabilities(false),
	)

	addTools(s, registry)

	if err := server.ServeStdio(s); err != nil {
		fmt.Printf("Failed to start MCP server: %v\n", err)
		os.Exit(1)
	}
}
