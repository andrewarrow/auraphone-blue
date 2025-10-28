package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/user/auraphone-blue/gui"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/testreport"
)

func main() {
	// Parse CLI flags
	headless := flag.Bool("headless", false, "Run in headless mode without GUI")
	numPhones := flag.Int("phones", 0, "Number of phones to auto-launch (default: 0 = launch GUI with no phones)")
	duration := flag.Duration("duration", 0, "How long to run the test (e.g., 120s, 5m). 0 means run until Ctrl+C")
	logLevel := flag.String("log-level", "TRACE", "Set log level (ERROR, WARN, INFO, DEBUG, TRACE)")
	testReport := flag.Bool("test-report", false, "Generate test report from data directory")
	dataDir := flag.String("data-dir", "", "Data directory for test report (default: ~/.auraphone-blue-data)")
	flag.Parse()

	fmt.Println("=== Auraphone Blue ===")

	// Test report mode - generate report and exit
	if *testReport {
		if err := testreport.Generate(*dataDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Set log level from CLI flag
	logger.SetLevel(logger.ParseLevel(*logLevel))
	fmt.Printf("Log level set to: %s\n", *logLevel)

	// Clean up old device directories from previous runs
	if err := gui.CleanupOldDevices(); err != nil {
		fmt.Printf("Warning: failed to cleanup old devices: %v\n", err)
	}

	// Run in headless mode if flag is provided
	if *headless {
		// Headless requires explicit phone count
		if *numPhones <= 0 {
			*numPhones = 4 // Default to 4 phones in headless mode
		}
		gui.RunStressTest(*numPhones, *duration)
		return
	}

	// If --phones flag is provided with value > 0, start phones automatically
	if *numPhones > 0 {
		gui.RunAutoStart(*numPhones, *duration, *logLevel)
		return
	}

	// Otherwise, run the normal GUI launcher with no phones
	fmt.Println("Starting launcher menu...")
	launcher := gui.NewLauncher(*logLevel)
	launcher.Run()
}
