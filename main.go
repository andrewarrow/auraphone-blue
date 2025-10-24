package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/user/auraphone-blue/android"
	"github.com/user/auraphone-blue/iphone"
	"github.com/user/auraphone-blue/phone"
)

// PhoneWindow represents a single phone instance with its own window
type PhoneWindow struct {
	window            fyne.Window
	currentTab        string
	app               fyne.App
	devicesMap        map[string]phone.DiscoveredDevice // Device ID -> Device
	discoveredDevices []phone.DiscoveredDevice          // Sorted list for UI
	devicesMutex      sync.RWMutex
	deviceListWidget  *widget.List
	phone             phone.Phone
	contentArea       *fyne.Container
	updateContentFunc func(string)
	needsRefresh      bool
	selectedPhoto     string                 // Currently selected profile photo
	profileImage      *canvas.Image          // Profile tab image
	deviceImages      map[string]image.Image // Device ID -> profile image cache
}

// NewPhoneWindow creates a new phone window
func NewPhoneWindow(app fyne.App, platformType string) *PhoneWindow {
	// Select random initial photo from face1.jpg to face12.jpg
	photoNum := rand.Intn(12) + 1 // Random number from 1 to 12
	selectedPhoto := fmt.Sprintf("testdata/face%d.jpg", photoNum)

	pw := &PhoneWindow{
		currentTab:        "home",
		app:               app,
		devicesMap:        make(map[string]phone.DiscoveredDevice),
		discoveredDevices: []phone.DiscoveredDevice{},
		needsRefresh:      false,
		selectedPhoto:     selectedPhoto,
		deviceImages:      make(map[string]image.Image),
	}

	// Create platform-specific phone
	if platformType == "iOS" {
		pw.phone = iphone.NewIPhone()
	} else {
		pw.phone = android.NewAndroid()
	}

	if pw.phone == nil {
		fmt.Printf("Failed to create phone\n")
		return nil
	}

	// Set discovery callback
	pw.phone.SetDiscoveryCallback(pw.onDeviceDiscovered)

	// Create window
	deviceUUID := pw.phone.GetDeviceUUID()
	deviceName := pw.phone.GetDeviceName()
	pw.window = app.NewWindow(fmt.Sprintf("Auraphone - %s (%s)", deviceName, deviceUUID[:8]))
	pw.window.SetContent(pw.buildUI())
	pw.window.Resize(fyne.NewSize(375, 667)) // iPhone-like dimensions

	// Cleanup on close
	pw.window.SetOnClosed(func() {
		pw.cleanup()
	})

	// Set initial profile photo
	if err := pw.phone.SetProfilePhoto(pw.selectedPhoto); err != nil {
		fmt.Printf("Failed to set initial profile photo: %v\n", err)
	}

	// Start BLE operations
	pw.phone.Start()

	// Start periodic UI refresh ticker
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for range ticker.C {
			if pw.needsRefresh && pw.currentTab == "home" && pw.deviceListWidget != nil {
				pw.devicesMutex.Lock()
				pw.needsRefresh = false
				pw.devicesMutex.Unlock()

				// Use fyne.Do to ensure thread-safe UI updates
				fyne.Do(func() {
					if pw.deviceListWidget != nil {
						pw.deviceListWidget.Refresh()
					}
				})
			}
		}
	}()

	return pw
}

// buildUI creates the phone UI with 5 tabs
func (pw *PhoneWindow) buildUI() fyne.CanvasObject {
	// Header with device info
	header := container.NewVBox(
		widget.NewLabelWithStyle(pw.phone.GetDeviceName(), fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel(fmt.Sprintf("UUID: %s", pw.phone.GetDeviceUUID()[:8])),
		widget.NewSeparator(),
	)

	// Tab content area
	pw.contentArea = container.NewMax()

	// Update content based on current tab
	pw.updateContentFunc = func(tabName string) {
		pw.currentTab = tabName
		pw.contentArea.Objects = []fyne.CanvasObject{pw.getTabContent(tabName)}
		pw.contentArea.Refresh()
	}

	// Initial content
	pw.updateContentFunc("home")

	// Bottom navigation bar with 5 tabs
	homeBtn := widget.NewButton("Home", func() { pw.updateContentFunc("home") })
	searchBtn := widget.NewButton("Search", func() { pw.updateContentFunc("search") })
	addBtn := widget.NewButton("Add", func() { pw.updateContentFunc("add") })
	playBtn := widget.NewButton("Play", func() { pw.updateContentFunc("play") })
	profileBtn := widget.NewButton("Profile", func() { pw.updateContentFunc("profile") })

	tabBar := container.NewGridWithColumns(5,
		homeBtn,
		searchBtn,
		addBtn,
		playBtn,
		profileBtn,
	)

	// Main layout
	return container.NewBorder(
		header,
		tabBar,
		nil, nil,
		pw.contentArea,
	)
}

// getTabContent returns the content for a specific tab
func (pw *PhoneWindow) getTabContent(tabName string) fyne.CanvasObject {
	// Create dark background
	bgColor := color.RGBA{R: 18, G: 18, B: 18, A: 255}
	bg := canvas.NewRectangle(bgColor)

	if tabName == "home" {
		// Create device list for Home tab
		pw.deviceListWidget = widget.NewList(
			func() int {
				pw.devicesMutex.RLock()
				defer pw.devicesMutex.RUnlock()
				return len(pw.discoveredDevices)
			},
			func() fyne.CanvasObject {
				// Template for each device row matching iOS design
				// Large bold name using canvas.Text for size control
				nameText := canvas.NewText("", color.RGBA{R: 0, G: 204, B: 255, A: 255}) // Cyan like iOS
				nameText.TextSize = 20
				nameText.TextStyle = fyne.TextStyle{Bold: true}

				// Device info lines in gray - separate text elements for each line
				deviceIDText := canvas.NewText("", color.RGBA{R: 150, G: 150, B: 150, A: 255})
				deviceIDText.TextSize = 11

				rssiText := canvas.NewText("", color.RGBA{R: 150, G: 150, B: 150, A: 255})
				rssiText.TextSize = 11

				// Profile image (will be updated with actual photo or circle)
				profileImage := canvas.NewImageFromImage(nil)
				profileImage.FillMode = canvas.ImageFillContain
				profileImage.SetMinSize(fyne.NewSize(60, 60))

				// Profile circle fallback (60x60)
				profileCircle := canvas.NewCircle(color.RGBA{R: 60, G: 60, B: 60, A: 255})
				profileCircle.StrokeColor = color.RGBA{R: 120, G: 120, B: 120, A: 255}
				profileCircle.StrokeWidth = 2
				profileCircle.Resize(fyne.NewSize(60, 60))

				// Stack image on top of circle (image will hide circle when loaded)
				profileStack := container.NewMax(profileCircle, profileImage)

				// Vertical stack for name and info lines
				textStack := container.NewVBox(
					nameText,
					deviceIDText,
					rssiText,
				)

				// Horizontal layout: profile stack + text stack
				row := container.NewBorder(nil, nil,
					container.NewPadded(profileStack),
					nil,
					textStack,
				)
				return row
			},
			func(id widget.ListItemID, obj fyne.CanvasObject) {
				pw.devicesMutex.RLock()
				defer pw.devicesMutex.RUnlock()

				if id < len(pw.discoveredDevices) {
					device := pw.discoveredDevices[id]
					row := obj.(*fyne.Container)

					// Get the text stack from the center of the border container
					textStack := row.Objects[0].(*fyne.Container)
					nameText := textStack.Objects[0].(*canvas.Text)
					deviceIDText := textStack.Objects[1].(*canvas.Text)
					rssiText := textStack.Objects[2].(*canvas.Text)

					// Get the profile stack from the left of the border container
					leftPadding := row.Objects[2].(*fyne.Container) // Left side
					profileStack := leftPadding.Objects[0].(*fyne.Container)
					profileImage := profileStack.Objects[1].(*canvas.Image)

					// Update profile image if available
					if img, exists := pw.deviceImages[device.DeviceID]; exists {
						profileImage.Image = img
						profileImage.Refresh()
					}

					// Set name with cyan color (matching iOS)
					nameText.Text = device.Name
					nameText.Refresh()

					// Set device info on separate lines
					deviceIDText.Text = fmt.Sprintf("Device: %s", device.DeviceID[:8])
					deviceIDText.Refresh()

					rssiText.Text = fmt.Sprintf("RSSI: %.0f dBm, Connected: Yes", device.RSSI)
					rssiText.Refresh()
				}
			},
		)

		// Always return the list widget, even if empty
		// The list will handle its own empty state
		return container.NewMax(bg, pw.deviceListWidget)
	}

	// Profile tab
	if tabName == "profile" {
		// Load and display the selected profile image
		if pw.profileImage == nil {
			pw.profileImage = canvas.NewImageFromFile(pw.selectedPhoto)
			pw.profileImage.FillMode = canvas.ImageFillContain
		}
		pw.profileImage.File = pw.selectedPhoto
		pw.profileImage.Refresh()

		// Set a fixed size for the profile image
		pw.profileImage.SetMinSize(fyne.NewSize(200, 200))

		// Create a large profile image container
		profileContainer := container.NewVBox(
			widget.NewLabel(""), // Spacer
			container.NewCenter(pw.profileImage),
			widget.NewLabel(""), // Spacer
		)

		// Create photo selector dropdown
		photoOptions := make([]string, 12)
		for i := 0; i < 12; i++ {
			photoOptions[i] = fmt.Sprintf("face%d.jpg", i+1)
		}

		photoSelect := widget.NewSelect(photoOptions, func(selected string) {
			// Update the selected photo
			pw.selectedPhoto = "testdata/" + selected
			pw.profileImage.File = pw.selectedPhoto
			pw.profileImage.Refresh()

			// Notify other phones of the photo change
			if err := pw.phone.SetProfilePhoto(pw.selectedPhoto); err != nil {
				fmt.Printf("Failed to update profile photo: %v\n", err)
			}
		})

		// Set initial value in the selector
		currentPhoto := filepath.Base(pw.selectedPhoto)
		photoSelect.SetSelected(currentPhoto)

		// Layout
		profileContent := container.NewVBox(
			profileContainer,
			widget.NewLabel(""),
			widget.NewLabelWithStyle("Select Profile Photo", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
			container.NewPadded(photoSelect),
		)

		return container.NewMax(bg, profileContent)
	}

	// Other tabs show placeholder
	label := widget.NewLabelWithStyle(
		fmt.Sprintf("%s View\n(Coming Soon)", tabName),
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)

	return container.NewMax(bg, container.NewCenter(label))
}

// Show displays the phone window
func (pw *PhoneWindow) Show() {
	pw.window.Show()
}

// onDeviceDiscovered is called when a device is discovered via BLE
func (pw *PhoneWindow) onDeviceDiscovered(device phone.DiscoveredDevice) {
	pw.devicesMutex.Lock()
	defer pw.devicesMutex.Unlock()

	// Add or update device in map (deduplicates by ID)
	pw.devicesMap[device.DeviceID] = device

	// If device has a photo hash, try to find matching local image
	if device.PhotoHash != "" {
		pw.loadDevicePhoto(device.DeviceID, device.PhotoHash)
	}

	// Rebuild sorted list from map
	pw.discoveredDevices = make([]phone.DiscoveredDevice, 0, len(pw.devicesMap))
	for _, dev := range pw.devicesMap {
		pw.discoveredDevices = append(pw.discoveredDevices, dev)
	}

	pw.sortAndRefreshDevices()
}

// loadDevicePhoto tries to find a local photo matching the hash
func (pw *PhoneWindow) loadDevicePhoto(deviceID, photoHash string) {
	// Check if we already have this image cached
	if _, exists := pw.deviceImages[deviceID]; exists {
		return
	}

	// Try to find matching photo in testdata
	for i := 1; i <= 12; i++ {
		photoPath := fmt.Sprintf("testdata/face%d.jpg", i)
		data, err := os.ReadFile(photoPath)
		if err != nil {
			continue
		}

		// Calculate hash
		hash := sha256.Sum256(data)
		hashStr := hex.EncodeToString(hash[:])

		if hashStr == photoHash {
			// Found matching photo!
			img, _, err := image.Decode(bytes.NewReader(data))
			if err == nil {
				pw.deviceImages[deviceID] = img
				fmt.Printf("[%s] Loaded profile photo for device %s (face%d.jpg)\n",
					pw.phone.GetDeviceUUID()[:8], deviceID[:8], i)
				return
			}
		}
	}
}

// sortAndRefreshDevices sorts devices by RSSI and refreshes the UI
func (pw *PhoneWindow) sortAndRefreshDevices() {
	sort.Slice(pw.discoveredDevices, func(i, j int) bool {
		return pw.discoveredDevices[i].RSSI > pw.discoveredDevices[j].RSSI
	})

	// Mark that we need a refresh (ticker will pick this up)
	pw.needsRefresh = true
}

// cleanup cleans up resources when window is closed
func (pw *PhoneWindow) cleanup() {
	fmt.Printf("Closing %s phone (UUID: %s)\n", pw.phone.GetPlatform(), pw.phone.GetDeviceUUID()[:8])
	pw.phone.Stop()
}

// Launcher creates the main menu window
type Launcher struct {
	app    fyne.App
	window fyne.Window
}

// NewLauncher creates a new launcher window
func NewLauncher() *Launcher {
	myApp := app.New()
	launcher := &Launcher{
		app:    myApp,
		window: myApp.NewWindow("Auraphone Blue - Launcher"),
	}

	launcher.window.SetContent(launcher.buildUI())
	launcher.window.Resize(fyne.NewSize(400, 300))
	launcher.window.CenterOnScreen()

	return launcher
}

// buildUI creates the launcher menu UI
func (l *Launcher) buildUI() fyne.CanvasObject {
	// Title
	title := widget.NewLabelWithStyle(
		"Auraphone Blue",
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)

	subtitle := widget.NewLabel("Fake Bluetooth Simulator")
	subtitle.Alignment = fyne.TextAlignCenter

	// Start iOS button
	iosBtn := widget.NewButton("Start iOS Device", func() {
		phoneWindow := NewPhoneWindow(l.app, "iOS")
		if phoneWindow != nil {
			phoneWindow.Show()
			fmt.Printf("Started iOS device (UUID: %s)\n", phoneWindow.phone.GetDeviceUUID()[:8])
		}
	})

	// Start Android button
	androidBtn := widget.NewButton("Start Android Device", func() {
		phoneWindow := NewPhoneWindow(l.app, "Android")
		if phoneWindow != nil {
			phoneWindow.Show()
			fmt.Printf("Started Android device (UUID: %s)\n", phoneWindow.phone.GetDeviceUUID()[:8])
		}
	})

	// Info text
	infoText := widget.NewLabel("Click a button to launch a new phone.\nClose a phone window to stop that device.")
	infoText.Wrapping = fyne.TextWrapWord
	infoText.Alignment = fyne.TextAlignCenter

	// Layout
	content := container.NewVBox(
		widget.NewLabel(""),
		title,
		subtitle,
		widget.NewSeparator(),
		widget.NewLabel(""),
		iosBtn,
		androidBtn,
		widget.NewLabel(""),
		widget.NewSeparator(),
		infoText,
	)

	return container.NewCenter(content)
}

// Run starts the launcher
func (l *Launcher) Run() {
	l.window.ShowAndRun()
}

// cleanupOldDevices removes all device directories from previous runs
func cleanupOldDevices() error {
	dataPath := "data"

	// Check if data directory exists
	if _, err := os.Stat(dataPath); os.IsNotExist(err) {
		return nil // Nothing to clean
	}

	// Remove all contents
	entries, err := os.ReadDir(dataPath)
	if err != nil {
		return fmt.Errorf("failed to read data directory: %w", err)
	}

	for _, entry := range entries {
		path := filepath.Join(dataPath, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			fmt.Printf("Warning: failed to remove %s: %v\n", path, err)
		}
	}

	fmt.Printf("Cleaned up %d old device directories\n", len(entries))
	return nil
}

func main() {
	fmt.Println("=== Auraphone Blue - Launcher ===")
	fmt.Println("Starting launcher menu...")

	// Clean up old device directories from previous runs
	if err := cleanupOldDevices(); err != nil {
		fmt.Printf("Warning: failed to cleanup old devices: %v\n", err)
	}

	launcher := NewLauncher()
	launcher.Run()
}
