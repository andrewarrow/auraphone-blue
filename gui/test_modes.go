package gui

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/user/auraphone-blue/iphone"
	"github.com/user/auraphone-blue/phone"
)

// RunAutoStart starts N phones with GUI, launching one iOS device every second
func RunAutoStart(numPhones int, duration time.Duration, logLevel string) {
	fmt.Println("=== Auraphone Blue - Auto Start Mode ===")
	fmt.Printf("Will start %d iOS devices (1 per second)...\n", numPhones)

	myApp := app.New()

	// Create launcher window (optional, just to show status)
	launcher := &Launcher{
		app:    myApp,
		window: myApp.NewWindow("Auraphone Blue - Auto Start"),
	}

	statusLabel := widget.NewLabel("Starting devices...")
	statusLabel.Alignment = fyne.TextAlignCenter

	content := container.NewVBox(
		widget.NewLabel(""),
		widget.NewLabelWithStyle("Auraphone Blue", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Auto Start Mode (iOS Only)"),
		widget.NewSeparator(),
		statusLabel,
	)

	launcher.window.SetContent(container.NewCenter(content))
	launcher.window.Resize(fyne.NewSize(400, 200))
	launcher.window.CenterOnScreen()
	launcher.window.Show()

	// Start phones one per second
	go func() {
		for i := 0; i < numPhones; i++ {
			// Only iOS for now
			platformType := "iOS"

			// Capture loop variable for closure
			currentIndex := i
			currentPlatform := platformType

			// All UI operations must happen on the main thread
			fyne.Do(func() {
				phoneWindow := NewPhoneWindow(myApp, currentPlatform)
				if phoneWindow != nil {
					phoneWindow.Show()
					statusMsg := fmt.Sprintf("Started %d/%d devices (%s)", currentIndex+1, numPhones, currentPlatform)
					fmt.Println(statusMsg)
					statusLabel.SetText(statusMsg)
				}
			})

			// Wait 1 second before starting next phone (except after last phone)
			if i < numPhones-1 {
				time.Sleep(1 * time.Second)
			}
		}

		finalMsg := fmt.Sprintf("All %d devices started!", numPhones)
		// Update status label (must be on UI thread)
		fyne.Do(func() {
			statusLabel.SetText(finalMsg)
		})
		fmt.Println(finalMsg)

		// If duration is specified, set up auto-shutdown
		if duration > 0 {
			go func() {
				fmt.Printf("Will run for %v...\n", duration)
				time.Sleep(duration)
				fmt.Println("Duration elapsed, shutting down...")
				myApp.Quit()
			}()
		}
	}()

	myApp.Run()
}

// RunStressTest runs N iOS phones in headless mode for specified duration
func RunStressTest(numPhones int, duration time.Duration) {
	fmt.Println("=== Auraphone Blue - Stress Test Mode ===")
	fmt.Printf("Starting %d iOS devices in headless mode for %v...\n", numPhones, duration)

	// Note: Log level is already set from CLI flag in main(), don't override it here

	// Create device manager for hardware UUIDs
	manager := GetHardwareUUIDManager()

	// Create iOS devices only
	phones := make([]phone.Phone, numPhones)
	for i := 0; i < numPhones; i++ {
		hardwareUUID, err := manager.AllocateNextUUID()
		if err != nil {
			fmt.Printf("ERROR: Failed to allocate UUID for phone %d: %v\n", i+1, err)
			return
		}

		// Only iOS for now
		var p phone.Phone
		var platform string
		p = iphone.NewIPhone(hardwareUUID)
		platform = "iOS"

		if p == nil {
			fmt.Printf("ERROR: Failed to create %s phone %d\n", platform, i+1)
			return
		}

		// Set profile photo (cycle through face1.jpg to face12.jpg)
		photoIndex := (i % 12) + 1
		photoPath := fmt.Sprintf("testdata/face%d.jpg", photoIndex)
		if err := p.SetProfilePhoto(photoPath); err != nil {
			fmt.Printf("Warning: Failed to set profile photo for %s phone %d: %v\n", platform, i+1, err)
		}

		// Generate and set random profile data for simulation (GUI only)
		// Real phones would have users enter this themselves
		allocatedCount := manager.GetAllocatedCount()
		profileData, err := GetProfileForIndex(allocatedCount)
		if err != nil {
			fmt.Printf("Warning: Failed to load profile data for phone %d: %v (using defaults)\n", i+1, err)
			profileData = ProfileData{
				FirstName: platform,
				LastName:  "User",
				Tagline:   "Tech enthusiast",
				Instagram: "@user",
				YouTube:   "TechTalks",
			}
		}

		// Set the generated profile data on the phone
		profile := map[string]string{
			"first_name": profileData.FirstName,
			"last_name":  profileData.LastName,
			"tagline":    profileData.Tagline,
			"insta":      profileData.Instagram,
			"youtube":    profileData.YouTube,
		}
		if err := p.UpdateLocalProfile(profile); err != nil {
			fmt.Printf("Warning: Failed to set profile for phone %d: %v\n", i+1, err)
		}

		// Start the phone
		p.Start()
		phones[i] = p
		fmt.Printf("Started %s phone %d (UUID: %s, Device ID: %s)\n",
			platform, i+1, hardwareUUID[:8], p.GetDeviceUUID()[:8])
	}

	fmt.Printf("\nAll %d devices started.\n", numPhones)

	// Wait for duration or interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	if duration > 0 {
		fmt.Printf("Running for %v (or press Ctrl+C to stop early)...\n", duration)
		timer := time.NewTimer(duration)
		select {
		case <-timer.C:
			fmt.Println("\nDuration elapsed, shutting down...")
		case <-sigChan:
			timer.Stop()
			fmt.Println("\nInterrupted, shutting down...")
		}
	} else {
		fmt.Println("Running until Ctrl+C...")
		<-sigChan
		fmt.Println("\nShutting down devices...")
	}

	// Stop all devices
	for i, p := range phones {
		if p != nil {
			p.Stop()
			fmt.Printf("Stopped phone %d\n", i+1)
		}
	}

	fmt.Println("Test completed.")
}
