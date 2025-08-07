package main

// import (
// 	"bufio"
// 	"fmt"
// 	"log"
// 	"os"
// 	"strings"
// )

// func main() {
// 	fmt.Println("=====================================")
// 	fmt.Println("RAFT CONSENSUS TESTING CLIENT")
// 	fmt.Println("=====================================")
// 	fmt.Println()
// 	fmt.Println("This client tests Raft leader election and leader-only operations.")
// 	fmt.Println("Make sure you have started 3 servers with 'make server' before running.")
// 	fmt.Println()

// 	// Initialize test configuration
// 	config := NewTestConfig()

// 	// Show menu
// 	for {
// 		showMenu()
// 		choice := getUserChoice()

// 		switch choice {
// 		case "1":
// 			RunScenario1(config)
// 			waitForContinue()
// 		case "2":
// 			RunScenario2(config)
// 			waitForContinue()
// 		case "3":
// 			fmt.Println("Running both scenarios sequentially...")
// 			RunScenario1(config)
// 			fmt.Println("\n" + strings.Repeat("=", 60))
// 			fmt.Println("MOVING TO SCENARIO 2")
// 			fmt.Println(strings.Repeat("=", 60))
// 			waitForContinue()
// 			RunScenario2(config)
// 			waitForContinue()
// 		case "4":
// 			config.PrintServerStatus()
// 			waitForContinue()
// 		case "5":
// 			quickOperationTest(config)
// 			waitForContinue()
// 		case "q", "Q":
// 			fmt.Println("Exiting test client...")
// 			return
// 		default:
// 			fmt.Println("Invalid choice. Please try again.")
// 		}
// 	}
// }

// func showMenu() {
// 	fmt.Println("\n========== TEST MENU ==========")
// 	fmt.Println("1. Scenario 1: Normal Operation (Leader Redirection)")
// 	fmt.Println("2. Scenario 2: Leader Failure (Election Recovery)")
// 	fmt.Println("3. Run Both Scenarios")
// 	fmt.Println("4. Check Server Status")
// 	fmt.Println("5. Quick Operation Test")
// 	fmt.Println("Q. Quit")
// 	fmt.Println("===============================")
// 	fmt.Print("Enter your choice: ")
// }

// func getUserChoice() string {
// 	scanner := bufio.NewScanner(os.Stdin)
// 	scanner.Scan()
// 	return strings.TrimSpace(scanner.Text())
// }

// func waitForContinue() {
// 	fmt.Print("\nPress Enter to return to menu...")
// 	bufio.NewScanner(os.Stdin).Scan()
// }

// // quickOperationTest performs a simple test to verify basic functionality
// func quickOperationTest(config *TestConfig) {
// 	fmt.Println("\n=== Quick Operation Test ===")
// 	fmt.Println("Testing basic store/read/delete cycle...")

// 	// Check server status first
// 	config.PrintServerStatus()
// 	aliveServers := config.FindAliveServers()

// 	if len(aliveServers) == 0 {
// 		fmt.Println("❌ No servers are responding!")
// 		return
// 	}

// 	fmt.Printf("✓ Found %d responding servers\n", len(aliveServers))

// 	testFile := "quick_test.txt"
// 	testData := []byte("Quick test file content - " + fmt.Sprint(len(aliveServers)) + " servers active")

// 	// Use first alive server
// 	serverAddr := aliveServers[0].Address

// 	fmt.Printf("Using server: %s\n", serverAddr)

// 	// Store
// 	fmt.Print("Store operation... ")
// 	resp, err := config.SendStoreFileRequest(serverAddr, testFile, testData)
// 	if err != nil {
// 		fmt.Printf("❌ Failed: %v\n", err)
// 		return
// 	}
// 	fmt.Printf("✓ Success (%s)\n", resp.Code)

// 	// Read
// 	fmt.Print("Read operation... ")
// 	resp, err = config.SendReadFileRequest(serverAddr, testFile)
// 	if err != nil {
// 		fmt.Printf("❌ Failed: %v\n", err)
// 		return
// 	}
// 	if resp.Code == "OK" {
// 		fmt.Printf("✓ Success - Content length: %d bytes\n", len(resp.Body))
// 	} else {
// 		fmt.Printf("❌ Failed with code: %s\n", resp.Code)
// 		return
// 	}

// 	// Delete
// 	fmt.Print("Delete operation... ")
// 	resp, err = config.SendDeleteFileRequest(serverAddr, testFile)
// 	if err != nil {
// 		fmt.Printf("❌ Failed: %v\n", err)
// 		return
// 	}
// 	fmt.Printf("✓ Success (%s)\n", resp.Code)

// 	// Verify delete
// 	fmt.Print("Verify deletion... ")
// 	resp, err = config.SendReadFileRequest(serverAddr, testFile)
// 	if err != nil {
// 		fmt.Printf("❌ Network error: %v\n", err)
// 		return
// 	}
// 	if resp.Code == "NOT_FOUND" {
// 		fmt.Println("✓ File correctly deleted")
// 	} else {
// 		fmt.Printf("❌ File still exists (code: %s)\n", resp.Code)
// 	}

// 	fmt.Println("\n✓ Quick test completed successfully!")
// }

// // Additional helper functions for more comprehensive testing
// func init() {
// 	// Set up logging
// 	log.SetFlags(log.LstdFlags | log.Lshortfile)
// 	log.SetPrefix("[TEST] ")
// }
