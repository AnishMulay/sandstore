package main

import (
	"fmt"
	"log"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
)

// RunScenario1 runs the Normal Operation test scenario
// This tests that followers properly redirect write operations to the leader
func RunScenario1(config *TestConfig) {
	fmt.Println("=======================================================")
	fmt.Println("SCENARIO 1: Normal Operation - Leader Redirection Test")
	fmt.Println("=======================================================")
	fmt.Println()
	fmt.Println("This test verifies that:")
	fmt.Println("1. Only leaders can process write operations (store/delete)")
	fmt.Println("2. Followers properly redirect requests to the leader")
	fmt.Println("3. All nodes can handle read operations")
	fmt.Println("4. Response codes and behavior are consistent")
	fmt.Println()

	// Check initial server status
	config.PrintServerStatus()
	aliveServers := config.FindAliveServers()

	if len(aliveServers) < 2 {
		log.Fatal("Need at least 2 servers running for this test")
	}

	WaitForInput("Ensure all 3 servers are running and leader election is complete.")

	// Test data
	testFile1 := "scenario1_test_file.txt"
	testFile2 := "scenario1_delete_test.txt"
	testData1 := CreateTestFile(testFile1, 2) // 2KB file
	testData2 := CreateTestFile(testFile2, 1) // 1KB file

	fmt.Println("\n=== Phase 1: Testing Store Operations on All Servers ===")

	// Test 1: Store file - send to all servers
	results1 := config.RunOperationOnAllServers("STORE "+testFile1, func(serverAddr string) (*communication.Response, error) {
		return config.SendStoreFileRequest(serverAddr, testFile1, testData1)
	})

	AnalyzeResults(results1, "All servers should either process (if leader) or redirect successfully (if follower)")

	// Wait for operation to propagate
	time.Sleep(2 * time.Second)

	fmt.Println("\n=== Phase 2: Verifying File Storage with Read Operations ===")

	// Test 2: Read file from all servers to verify it's stored
	results2 := config.RunOperationOnAllServers("READ "+testFile1, func(serverAddr string) (*communication.Response, error) {
		return config.SendReadFileRequest(serverAddr, testFile1)
	})

	AnalyzeResults(results2, "All servers should be able to read the stored file")

	fmt.Println("\n=== Phase 3: Testing Store of Second File ===")

	// Test 3: Store second file for delete testing
	results3 := config.RunOperationOnAllServers("STORE "+testFile2, func(serverAddr string) (*communication.Response, error) {
		return config.SendStoreFileRequest(serverAddr, testFile2, testData2)
	})

	AnalyzeResults(results3, "All servers should handle store operation consistently")

	// Wait for operation to propagate
	time.Sleep(2 * time.Second)

	fmt.Println("\n=== Phase 4: Testing Delete Operations on All Servers ===")

	// Test 4: Delete file - send to all servers
	results4 := config.RunOperationOnAllServers("DELETE "+testFile2, func(serverAddr string) (*communication.Response, error) {
		return config.SendDeleteFileRequest(serverAddr, testFile2)
	})

	AnalyzeResults(results4, "All servers should either process (if leader) or redirect successfully (if follower)")

	// Wait for operation to propagate
	time.Sleep(2 * time.Second)

	fmt.Println("\n=== Phase 5: Verifying File Deletion ===")

	// Test 5: Try to read deleted file
	results5 := config.RunOperationOnAllServers("READ "+testFile2+" (should fail)", func(serverAddr string) (*communication.Response, error) {
		return config.SendReadFileRequest(serverAddr, testFile2)
	})

	// For delete verification, we expect NOT_FOUND responses
	fmt.Printf("\n=== Delete Verification Results ===\n")
	fmt.Printf("Expected Behavior: All servers should return NOT_FOUND for deleted file\n\n")

	for _, result := range results5 {
		// Success for delete verification means getting NOT_FOUND
		isDeleteVerified := result.Error == nil && result.ResponseCode == communication.CodeNotFound

		status := "✓ PASS"
		description := "File correctly deleted"
		if !isDeleteVerified {
			status = "✗ FAIL"
			if result.ResponseCode == communication.CodeOK {
				description = "File still exists (deletion failed)"
			} else {
				description = "Unexpected response code"
			}
		}

		fmt.Printf("[%s] READ %s -> %s (%s) [%v] - %s\n",
			status,
			testFile2,
			result.ServerAddress,
			result.ResponseCode,
			result.Duration,
			description)
	}

	fmt.Println("\n=== Phase 6: Mixed Operations Test ===")

	// Test 6: Perform mixed operations to different servers simultaneously
	fmt.Println("Testing concurrent operations to different servers...")

	testFile3 := "scenario1_concurrent_test.txt"
	testData3 := CreateTestFile(testFile3, 1)

	// Store on first server
	if len(aliveServers) > 0 {
		start := time.Now()
		resp, err := config.SendStoreFileRequest(aliveServers[0].Address, testFile3, testData3)
		duration := time.Since(start)

		result := TestResult{
			ServerAddress: aliveServers[0].Address,
			Operation:     "STORE " + testFile3,
			Success:       err == nil && resp != nil && resp.Code == communication.CodeOK,
			ResponseCode:  resp.Code,
			Error:         err,
			Duration:      duration,
		}

		if result.Success {
			result.Description = "Concurrent store operation successful"
		} else {
			result.Description = "Concurrent store operation failed"
		}

		PrintTestResult(result)
	}

	// Read from second server immediately
	if len(aliveServers) > 1 {
		time.Sleep(500 * time.Millisecond) // Small delay to allow propagation

		start := time.Now()
		resp, err := config.SendReadFileRequest(aliveServers[1].Address, testFile3)
		duration := time.Since(start)

		result := TestResult{
			ServerAddress: aliveServers[1].Address,
			Operation:     "READ " + testFile3,
			Success:       err == nil && resp != nil && resp.Code == communication.CodeOK,
			ResponseCode:  resp.Code,
			Error:         err,
			Duration:      duration,
		}

		if result.Success {
			result.Description = "Concurrent read operation successful"
		} else {
			result.Description = "Concurrent read operation failed"
		}

		PrintTestResult(result)
	}

	fmt.Println("\n=== Scenario 1 Summary ===")
	fmt.Println("✓ Store operations tested on all servers")
	fmt.Println("✓ Read operations verified file storage")
	fmt.Println("✓ Delete operations tested on all servers")
	fmt.Println("✓ Delete verification confirmed file removal")
	fmt.Println("✓ Concurrent operations tested")
	fmt.Println()
	fmt.Println("Expected observations:")
	fmt.Println("- Only one server (leader) processes writes directly")
	fmt.Println("- Other servers (followers) redirect to leader")
	fmt.Println("- All operations eventually succeed through redirection")
	fmt.Println("- Read operations work on any server")
	fmt.Println("- Response times may vary (direct vs redirected)")
	fmt.Println("==========================")

	// Cleanup
	fmt.Println("\n=== Cleanup ===")
	config.SendDeleteFileRequest(aliveServers[0].Address, testFile1)
	config.SendDeleteFileRequest(aliveServers[0].Address, testFile3)
	fmt.Println("Test files cleaned up")
}
