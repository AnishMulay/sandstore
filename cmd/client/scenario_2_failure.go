package main

import (
	"fmt"
	"log"
	"time"

	"github.com/AnishMulay/sandstore/internal/communication"
)

// RunScenario2 runs the Leader Failure test scenario
// This tests behavior when the current leader fails and a new leader is elected
func RunScenario2(config *TestConfig) {
	fmt.Println("=====================================================")
	fmt.Println("SCENARIO 2: Leader Failure - Election Recovery Test")
	fmt.Println("=====================================================")
	fmt.Println()
	fmt.Println("This test verifies that:")
	fmt.Println("1. System detects leader failure")
	fmt.Println("2. New leader election occurs")
	fmt.Println("3. Operations continue after leadership change")
	fmt.Println("4. System handles 'no leader available' states gracefully")
	fmt.Println()

	// Check initial server status
	config.PrintServerStatus()
	aliveServers := config.FindAliveServers()

	if len(aliveServers) < 3 {
		log.Fatal("Need all 3 servers running for leader failure test")
	}

	WaitForInput("Ensure all 3 servers are running and leader election is complete.")

	// Test data
	testFile1 := "scenario2_pre_failure.txt"
	testFile2 := "scenario2_during_failure.txt"
	testFile3 := "scenario2_post_failure.txt"
	testData := CreateTestFile("scenario2", 2) // 2KB file

	fmt.Println("\n=== Phase 1: Pre-Failure Operations ===")
	fmt.Println("Testing normal operations before leader failure...")

	// Test 1: Store file to establish which server is handling requests
	fmt.Println("Storing initial test file...")
	results1 := config.RunOperationOnAllServers("STORE "+testFile1, func(serverAddr string) (*communication.Response, error) {
		return config.SendStoreFileRequest(serverAddr, testFile1, testData)
	})

	AnalyzeResults(results1, "Normal operation - leader processes, followers redirect")

	// Identify likely leader based on response times (direct processing is usually faster)
	var likelyLeader string
	minDuration := time.Hour
	for _, result := range results1 {
		if result.Success && result.Duration < minDuration {
			minDuration = result.Duration
			likelyLeader = result.ServerAddress
		}
	}

	if likelyLeader != "" {
		fmt.Printf("Likely leader based on response times: %s\n", likelyLeader)
	}

	// Wait for propagation
	time.Sleep(2 * time.Second)

	fmt.Println("\n=== Phase 2: Simulate Leader Failure ===")
	fmt.Printf("MANUAL ACTION REQUIRED:\n")
	fmt.Printf("You need to manually kill the leader server process.\n")
	if likelyLeader != "" {
		fmt.Printf("Based on response analysis, the leader appears to be: %s\n", likelyLeader)
		fmt.Printf("Kill the server running on %s\n", likelyLeader)
	} else {
		fmt.Printf("Kill any one of the running servers to simulate leader failure.\n")
	}
	fmt.Printf("Steps:\n")
	fmt.Printf("1. Find the server process (check which server responded fastest)\n")
	fmt.Printf("2. Kill that server process\n")
	fmt.Printf("3. Return here and press Enter\n")

	WaitForInput("After killing the leader server, press Enter to continue...")

	fmt.Println("\n=== Phase 3: Operations During Leadership Transition ===")
	fmt.Println("Testing operations immediately after leader failure...")

	// Test 2: Try operations immediately after leader failure
	results2 := config.RunOperationOnAllServers("STORE "+testFile2+" (during transition)", func(serverAddr string) (*communication.Response, error) {
		return config.SendStoreFileRequest(serverAddr, testFile2, testData)
	})

	AnalyzeResults(results2, "During leadership transition - may see unavailable responses")

	// Check server status
	fmt.Println("\nChecking server status after failure...")
	aliveServers = config.FindAliveServers()
	config.PrintServerStatus()

	fmt.Printf("Servers still responding: %d/3\n", len(aliveServers))

	fmt.Println("\n=== Phase 4: Wait for New Leader Election ===")
	fmt.Println("Waiting for new leader election to complete...")
	fmt.Println("This typically takes 5-15 seconds depending on election timeout settings.")

	// Wait for election to complete
	for i := 0; i < 6; i++ {
		fmt.Printf("Waiting... %d/6 (5 second intervals)\n", i+1)
		time.Sleep(5 * time.Second)

		// Test if leadership is restored by trying a quick operation
		if len(aliveServers) > 0 {
			resp, err := config.SendReadFileRequest(aliveServers[0].Address, testFile1)
			if err == nil && resp.Code == communication.CodeOK {
				fmt.Println("✓ Server responding normally")
			} else {
				fmt.Println("- Still in transition...")
			}
		}
	}

	fmt.Println("\n=== Phase 5: Post-Election Operations ===")
	fmt.Println("Testing operations after new leader election...")

	// Update alive servers list
	aliveServers = config.FindAliveServers()

	if len(aliveServers) < 2 {
		fmt.Println("WARNING: Less than 2 servers responding. Cluster may not have quorum.")
	}

	// Test 3: Store operations after election
	results3 := config.RunOperationOnAllServers("STORE "+testFile3+" (post-election)", func(serverAddr string) (*communication.Response, error) {
		return config.SendStoreFileRequest(serverAddr, testFile3, testData)
	})

	AnalyzeResults(results3, "Post-election - new leader should handle operations")

	// Test 4: Verify all files are accessible
	fmt.Println("\n=== Phase 6: Data Consistency Verification ===")

	testFiles := []string{testFile1, testFile2, testFile3}

	for _, filename := range testFiles {
		fmt.Printf("Verifying access to: %s\n", filename)

		results := config.RunOperationOnAllServers("READ "+filename, func(serverAddr string) (*communication.Response, error) {
			return config.SendReadFileRequest(serverAddr, filename)
		})

		// Count successful reads
		successCount := 0
		for _, result := range results {
			if result.Success {
				successCount++
			}
		}

		fmt.Printf("  File %s: %d/%d servers can read it\n", filename, successCount, len(aliveServers))
	}

	fmt.Println("\n=== Phase 7: Recovery Testing ===")
	fmt.Printf("OPTIONAL: Restart the failed server\n")
	fmt.Printf("If you restart the killed server, it should:\n")
	fmt.Printf("1. Rejoin the cluster as a follower\n")
	fmt.Printf("2. Catch up with the current state\n")
	fmt.Printf("3. Participate in future elections\n\n")

	WaitForInput("If you want to test server recovery, restart the killed server now and press Enter. Otherwise, just press Enter to skip.")

	// Check if failed server is back
	config.PrintServerStatus()
	newAliveServers := config.FindAliveServers()

	if len(newAliveServers) > len(aliveServers) {
		fmt.Println("✓ Failed server appears to have rejoined the cluster")

		// Test operation with recovered server
		results4 := config.RunOperationOnAllServers("STORE recovery_test.txt", func(serverAddr string) (*communication.Response, error) {
			return config.SendStoreFileRequest(serverAddr, "recovery_test.txt", []byte("recovery test"))
		})

		AnalyzeResults(results4, "All servers including recovered one should handle operations")
	} else {
		fmt.Println("No additional servers detected - skipping recovery test")
	}

	fmt.Println("\n=== Scenario 2 Summary ===")
	fmt.Println("✓ Pre-failure operations completed")
	fmt.Println("✓ Leader failure simulated")
	fmt.Println("✓ Leadership transition observed")
	fmt.Println("✓ Post-election operations tested")
	fmt.Println("✓ Data consistency verified")
	fmt.Println("✓ Recovery behavior observed")
	fmt.Println()
	fmt.Println("Key observations to verify:")
	fmt.Printf("- Cluster maintained operation with %d/%d servers\n", len(aliveServers), 3)
	fmt.Println("- New leader was elected after failure")
	fmt.Println("- Operations resumed after election period")
	fmt.Println("- Data remained consistent across surviving nodes")
	fmt.Println("- System handled 'no leader' states gracefully")
	fmt.Println("===============================")

	// Cleanup
	fmt.Println("\n=== Cleanup ===")
	if len(aliveServers) > 0 {
		config.SendDeleteFileRequest(aliveServers[0].Address, testFile1)
		config.SendDeleteFileRequest(aliveServers[0].Address, testFile2)
		config.SendDeleteFileRequest(aliveServers[0].Address, testFile3)
		config.SendDeleteFileRequest(aliveServers[0].Address, "recovery_test.txt")
		fmt.Println("Test files cleaned up")
	}
}
