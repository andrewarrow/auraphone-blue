package main

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/user/auraphone-blue/wire"
)

// PhoneWindow represents a single phone instance with its own window
type PhoneWindow struct {
	window      fyne.Window
	deviceUUID  string
	deviceName  string
	platform    string
	wire        *wire.Wire
	currentTab  string
	app         fyne.App
}

// NewPhoneWindow creates a new phone window
func NewPhoneWindow(app fyne.App, platform string) *PhoneWindow {
	deviceUUID := uuid.New().String()
	deviceName := fmt.Sprintf("%s Device", platform)

	pw := &PhoneWindow{
		deviceUUID: deviceUUID,
		deviceName: deviceName,
		platform:   platform,
		currentTab: "home",
		app:        app,
	}

	// Initialize wire for this device
	pw.wire = wire.NewWire(deviceUUID)
	if err := pw.wire.InitializeDevice(); err != nil {
		fmt.Printf("Failed to initialize device: %v\n", err)
		return nil
	}

	// Create window
	pw.window = app.NewWindow(fmt.Sprintf("Auraphone - %s (%s)", deviceName, deviceUUID[:8]))
	pw.window.SetContent(pw.buildUI())
	pw.window.Resize(fyne.NewSize(375, 667)) // iPhone-like dimensions

	// Cleanup on close
	pw.window.SetOnClosed(func() {
		pw.cleanup()
	})

	return pw
}

// buildUI creates the phone UI with 5 tabs
func (pw *PhoneWindow) buildUI() fyne.CanvasObject {
	// Header with device info
	header := container.NewVBox(
		widget.NewLabelWithStyle(pw.deviceName, fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel(fmt.Sprintf("UUID: %s", pw.deviceUUID[:8])),
		widget.NewSeparator(),
	)

	// Tab content area
	contentArea := container.NewMax()

	// Update content based on current tab
	updateContent := func(tabName string) {
		pw.currentTab = tabName
		contentArea.Objects = []fyne.CanvasObject{pw.getTabContent(tabName)}
		contentArea.Refresh()
	}

	// Initial content
	updateContent("home")

	// Bottom navigation bar with 5 tabs
	homeBtn := widget.NewButton("Home", func() { updateContent("home") })
	searchBtn := widget.NewButton("Search", func() { updateContent("search") })
	addBtn := widget.NewButton("Add", func() { updateContent("add") })
	playBtn := widget.NewButton("Play", func() { updateContent("play") })
	profileBtn := widget.NewButton("Profile", func() { updateContent("profile") })

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
		contentArea,
	)
}

// getTabContent returns the content for a specific tab
func (pw *PhoneWindow) getTabContent(tabName string) fyne.CanvasObject {
	// Create empty view with tab name
	bgColor := color.RGBA{R: 245, G: 245, B: 245, A: 255}
	bg := canvas.NewRectangle(bgColor)

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

// cleanup cleans up resources when window is closed
func (pw *PhoneWindow) cleanup() {
	fmt.Printf("Closing %s phone (UUID: %s)\n", pw.platform, pw.deviceUUID[:8])
	// TODO: Add BLE cleanup when we add BLE functionality
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
		phone := NewPhoneWindow(l.app, "iOS")
		if phone != nil {
			phone.Show()
			fmt.Printf("Started iOS device (UUID: %s)\n", phone.deviceUUID[:8])
		}
	})

	// Start Android button
	androidBtn := widget.NewButton("Start Android Device", func() {
		phone := NewPhoneWindow(l.app, "Android")
		if phone != nil {
			phone.Show()
			fmt.Printf("Started Android device (UUID: %s)\n", phone.deviceUUID[:8])
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
