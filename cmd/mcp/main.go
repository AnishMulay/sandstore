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
}

func main() {
	// Create a new MCP server
	s := server.NewMCPServer(
		"Demo 🚀",
		"1.0.0",
		server.WithToolCapabilities(false),
	)

	// Add tool
	tool := mcp.NewTool("hello_world",
		mcp.WithDescription("Say hello to someone"),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Name of the person to greet"),
		),
	)

	// Add tool handler
	s.AddTool(tool, helloHandler)

	// Start the stdio server
	if err := server.ServeStdio(s); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}
}

func helloHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := request.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Hello, %s!", name)), nil
}
