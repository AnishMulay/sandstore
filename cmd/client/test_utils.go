package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
	"github.com/AnishMulay/sandstore/internal/log_service"
)

// Server represents a Raft server node for testing
type Server struct {
	ID      string
	Address string
	IsAlive bool
}

// TestConfig holds configuration for test scenarios
type TestConfig struct {
	Servers     []Server
	ClientComm  communication.Communicator
	LogService  log_service.LogService
	Context     context.Context
	TestTimeout time.Duration
}

// NewTestConfig creates a new test configuration
func NewTestConfig() *TestConfig {
	logDir := "./logs"
	nodeID := "test_client"
	ls := log_service.NewLocalDiscLogService(logDir, nodeID, "ERROR") // Error-only logging
	comm := communication.NewGRPCCommunicator(":8090", ls)

	servers := []Server{
		{ID: "node1", Address: "localhost:8080", IsAlive: true},
		{ID: "node2", Address: "localhost:8081", IsAlive: true},
		{ID: "node3", Address: "localhost:8082", IsAlive: true},
	}

	return &TestConfig{
		Servers:     servers,
		ClientComm:  comm,
		LogService:  ls,
		Context:     context.Background(),
		TestTimeout: 30 * time.Second,
	}
}

// SendStoreFileRequest sends a store file request to the specified server
func (tc *TestConfig) SendStoreFileRequest(serverAddr, filePath string, data []byte) (*communication.Response, error) {
	storeRequest := communication.StoreFileRequest{
		Path: filePath,
		Data: data,
	}

	msg := communication.Message{
		From:    "test_client",
		Type:    communication.MessageTypeStoreFile,
		Payload: storeRequest,
	}

	log.Printf("Sending STORE request to %s for file: %s (%d bytes)", serverAddr, filePath, len(data))
	return tc.ClientComm.Send(tc.Context, serverAddr, msg)
}

// SendDeleteFileRequest sends a delete file request to the specified server
func (tc *TestConfig) SendDeleteFileRequest(serverAddr, filePath string) (*communication.Response, error) {
	deleteRequest := communication.DeleteFileRequest{
		Path: filePath,
	}

	msg := communication.Message{
		From:    "test_client",
		Type:    communication.MessageTypeDeleteFile,
		Payload: deleteRequest,
	}

	log.Printf("Sending DELETE request to %s for file: %s", serverAddr, filePath)
	return tc.ClientComm.Send(tc.Context, serverAddr, msg)
}

// SendReadFileRequest sends a read file request to the specified server
func (tc *TestConfig) SendReadFileRequest(serverAddr, filePath string) (*communication.Response, error) {
	readRequest := communication.ReadFileRequest{
		Path: filePath,
	}

	msg := communication.Message{
		From:    "test_client",
		Type:    communication.MessageTypeReadFile,
		Payload: readRequest,
	}

	log.Printf("Sending READ request to %s for file: %s", serverAddr, filePath)
	return tc.ClientComm.Send(tc.Context, serverAddr, msg)
}

// TestResult represents the outcome of a test operation
type TestResult struct {
	ServerAddress string
	Operation     string
	Success       bool
	ResponseCode  communication.SandCode
	Error         error
	Description   string
	Duration      time.Duration
}

// PrintTestResult prints a formatted test result
func PrintTestResult(result TestResult) {
	status := "✓ PASS"
	if !result.Success {
		status = "✗ FAIL"
	}

	fmt.Printf("[%s] %s -> %s (%s) [%v] - %s\n",
		status,
		result.Operation,
		result.ServerAddress,
		result.ResponseCode,
		result.Duration,
		result.Description)

	if result.Error != nil {
		fmt.Printf("    Error: %v\n", result.Error)
	}
}

// WaitForInput prompts user and waits for Enter key
func WaitForInput(message string) {
	fmt.Printf("\n%s\nPress Enter to continue...", message)
	var input string
	fmt.Scanln(&input)
}

// CreateTestFile generates test file data
func CreateTestFile(filename string, sizeKB int) []byte {
	content := fmt.Sprintf("Test file: %s\nCreated at: %s\n", filename, time.Now().Format(time.RFC3339))

	// Pad to desired size
	padding := strings.Repeat("A", (sizeKB*1024)-len(content))
	return []byte(content + padding)
}

// IsServerAlive checks if a server is responding
func (tc *TestConfig) IsServerAlive(serverAddr string) bool {
	testFile := "health_check.txt"

	resp, err := tc.SendReadFileRequest(serverAddr, testFile)
	return err == nil && (resp.Code == communication.CodeOK || resp.Code == communication.CodeInternal)
}

// FindAliveServers returns a list of servers that are responding
func (tc *TestConfig) FindAliveServers() []Server {
	var aliveServers []Server

	for _, server := range tc.Servers {
		if tc.IsServerAlive(server.Address) {
			server.IsAlive = true
			aliveServers = append(aliveServers, server)
		} else {
			server.IsAlive = false
		}
	}

	return aliveServers
}

// PrintServerStatus prints the status of all servers
func (tc *TestConfig) PrintServerStatus() {
	fmt.Println("\n=== Server Status ===")
	for _, server := range tc.Servers {
		status := "ALIVE"
		if !tc.IsServerAlive(server.Address) {
			status = "DOWN/UNREACHABLE"
		}
		fmt.Printf("%s (%s): %s\n", server.ID, server.Address, status)
	}
	fmt.Println("====================")
}

// RunOperationOnAllServers runs the same operation on all servers and returns results
func (tc *TestConfig) RunOperationOnAllServers(operation string, operationFunc func(string) (*communication.Response, error)) []TestResult {
	var results []TestResult

	for _, server := range tc.Servers {
		start := time.Now()
		resp, err := operationFunc(server.Address)
		duration := time.Since(start)

		result := TestResult{
			ServerAddress: server.Address,
			Operation:     operation,
			Success:       err == nil && resp != nil && resp.Code == communication.CodeOK,
			Duration:      duration,
		}

		if err != nil {
			result.Error = err
			result.Description = "Network/Communication error"
		} else if resp != nil {
			result.ResponseCode = resp.Code
			if resp.Code == communication.CodeOK {
				result.Description = "Operation successful"
			} else if resp.Code == communication.CodeUnavailable {
				result.Description = "Server unavailable (possibly no leader)"
			} else {
				result.Description = fmt.Sprintf("Operation failed with code: %s", resp.Code)
			}
		} else {
			result.Description = "No response received"
		}

		results = append(results, result)
	}

	return results
}

// AnalyzeResults analyzes test results and prints summary
func AnalyzeResults(results []TestResult, expectedBehavior string) {
	fmt.Printf("\n=== Test Results Analysis ===\n")
	fmt.Printf("Expected Behavior: %s\n\n", expectedBehavior)

	successCount := 0
	for _, result := range results {
		PrintTestResult(result)
		if result.Success {
			successCount++
		}
	}

	fmt.Printf("\nSummary: %d/%d operations successful\n", successCount, len(results))
	fmt.Println("=============================")
}
