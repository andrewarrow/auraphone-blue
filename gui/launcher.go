package gui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// Launcher creates the main menu window
type Launcher struct {
	app    fyne.App
	window fyne.Window
}

// NewLauncher creates a new launcher window with the specified initial log level
func NewLauncher(initialLogLevel string) *Launcher {
	myApp := app.New()
	launcher := &Launcher{
		app:    myApp,
		window: myApp.NewWindow("Auraphone Blue - Launcher"),
	}

	launcher.window.SetContent(launcher.buildUI(initialLogLevel))
	launcher.window.Resize(fyne.NewSize(400, 300))
	launcher.window.CenterOnScreen()

	return launcher
}

// buildUI creates the launcher menu UI with the specified initial log level
func (l *Launcher) buildUI(initialLogLevel string) fyne.CanvasObject {
	// Title
	title := widget.NewLabelWithStyle(
		"Auraphone Blue",
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)

	subtitle := widget.NewLabel("Fake Bluetooth Simulator")
	subtitle.Alignment = fyne.TextAlignCenter

	// Log level display (read-only, set via CLI flag)
	logLevelLabel := widget.NewLabel("Log Level:")
	logLevelDisplay := widget.NewLabel(initialLogLevel)
	logLevelDisplay.TextStyle = fyne.TextStyle{Bold: true}

	// Start iOS button
	iosBtn := widget.NewButton("Start iOS Device", func() {
		// Small delay to ensure unique random seed if multiple devices started quickly
		time.Sleep(10 * time.Millisecond)
		phoneWindow := NewPhoneWindow(l.app, "iOS")
		if phoneWindow != nil {
			phoneWindow.Show()
			fmt.Printf("Started iOS device (UUID: %s)\n", phoneWindow.phone.GetDeviceUUID()[:8])
		}
	})

	// Start Android button
	androidBtn := widget.NewButton("Start Android Device", func() {
		// Small delay to ensure unique random seed if multiple devices started quickly
		time.Sleep(10 * time.Millisecond)
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
		container.NewHBox(logLevelLabel, logLevelDisplay),
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
