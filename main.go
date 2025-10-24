package main

import (
	"fmt"
	"image/color"
	"sort"
	"sync"

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
}

// NewPhoneWindow creates a new phone window
func NewPhoneWindow(app fyne.App, platformType string) *PhoneWindow {
	pw := &PhoneWindow{
		currentTab:        "home",
		app:               app,
		devicesMap:        make(map[string]phone.DiscoveredDevice),
		discoveredDevices: []phone.DiscoveredDevice{},
		needsRefresh:      false,
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

	// Start BLE operations
	pw.phone.Start()

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
				// Template for each device row
				nameLabel := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
				nameLabel.TextStyle.Bold = true
				infoLabel := widget.NewLabel("")
				infoLabel.TextStyle.Italic = true

				profileCircle := canvas.NewCircle(color.RGBA{R: 60, G: 60, B: 60, A: 255})
				profileCircle.StrokeColor = color.RGBA{R: 120, G: 120, B: 120, A: 255}
				profileCircle.StrokeWidth = 2

				row := container.NewHBox(
					container.NewPadded(profileCircle),
					container.NewVBox(nameLabel, infoLabel),
				)
				return row
			},
			func(id widget.ListItemID, obj fyne.CanvasObject) {
				pw.devicesMutex.RLock()
				defer pw.devicesMutex.RUnlock()

				if id < len(pw.discoveredDevices) {
					device := pw.discoveredDevices[id]
					row := obj.(*fyne.Container)
					textContainer := row.Objects[1].(*fyne.Container)
					nameLabel := textContainer.Objects[0].(*widget.Label)
					infoLabel := textContainer.Objects[1].(*widget.Label)

					nameLabel.SetText(device.Name)
					nameLabel.TextStyle.Bold = true
					nameLabel.Refresh()

					infoLabel.SetText(fmt.Sprintf("Device: %s\nRSSI: %.0f dBm", device.DeviceID[:8], device.RSSI))
					infoLabel.Refresh()
				}
			},
		)

		// Always return the list widget, even if empty
		// The list will handle its own empty state
		return container.NewMax(bg, pw.deviceListWidget)
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

	// Rebuild sorted list from map
	pw.discoveredDevices = make([]phone.DiscoveredDevice, 0, len(pw.devicesMap))
	for _, dev := range pw.devicesMap {
		pw.discoveredDevices = append(pw.discoveredDevices, dev)
	}

	pw.sortAndRefreshDevices()
}

// sortAndRefreshDevices sorts devices by RSSI and refreshes the UI
func (pw *PhoneWindow) sortAndRefreshDevices() {
	sort.Slice(pw.discoveredDevices, func(i, j int) bool {
		return pw.discoveredDevices[i].RSSI > pw.discoveredDevices[j].RSSI
	})

	// Mark that we need a refresh
	pw.needsRefresh = true

	// If on home tab and widget exists, trigger refresh
	if pw.currentTab == "home" && pw.deviceListWidget != nil {
		// Refresh the list widget - this is safe to call from any goroutine
		pw.deviceListWidget.Refresh()
	}
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

func main() {
	fmt.Println("=== Auraphone Blue - Launcher ===")
	fmt.Println("Starting launcher menu...")

	launcher := NewLauncher()
	launcher.Run()
}
