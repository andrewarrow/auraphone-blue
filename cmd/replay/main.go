package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/user/auraphone-blue/tests"
)

func main() {
	scenarioPath := flag.String("scenario", "", "Path to scenario JSON file")
	flag.Parse()

	if *scenarioPath == "" {
		fmt.Println("Usage: replay --scenario <path-to-scenario.json>")
		fmt.Println("\nExample:")
		fmt.Println("  go run cmd/replay/main.go --scenario scenarios/photo_collision.json")
		os.Exit(1)
	}

	// Load scenario
	scenario, err := tests.LoadScenario(*scenarioPath)
	if err != nil {
		log.Fatalf("Failed to load scenario: %v", err)
	}

	fmt.Printf("=== Running Scenario: %s ===\n", scenario.Name)
	fmt.Printf("Description: %s\n", scenario.Description)
	fmt.Printf("Devices: %d\n", len(scenario.Devices))
	fmt.Printf("Events: %d\n", len(scenario.Timeline))
	fmt.Printf("Duration: %v\n\n", scenario.Duration())

	// Validate scenario
	errors := scenario.Validate()
	if len(errors) > 0 {
		fmt.Println("❌ Scenario validation failed:")
		for _, err := range errors {
			fmt.Printf("  - %s\n", err)
		}
		os.Exit(1)
	}

	// Create runner
	runner := tests.NewScenarioRunner(scenario)

	// Setup devices
	fmt.Println("Setting up devices...")
	if err := runner.Setup(); err != nil {
		log.Fatalf("Failed to setup scenario: %v", err)
	}
	fmt.Println("✓ Devices initialized\n")

	// Run scenario
	fmt.Println("Executing timeline...")
	if err := runner.Run(); err != nil {
		log.Fatalf("Failed to run scenario: %v", err)
	}

	// Wait a bit for any async operations
	fmt.Println("\nWaiting for completion...")
	// time.Sleep(2 * time.Second)

	// Check assertions
	fmt.Println("\nChecking assertions...")
	results := runner.CheckAssertions()

	// Print report
	runner.PrintReport()

	// Exit with error if any assertions failed
	allPassed := true
	for _, result := range results {
		if !result.Passed {
			allPassed = false
			break
		}
	}

	if allPassed {
		fmt.Println("\n✅ All assertions passed!")
		os.Exit(0)
	} else {
		fmt.Println("\n❌ Some assertions failed")
		os.Exit(1)
	}
}
