package gui

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/user/auraphone-blue/android"
	"github.com/user/auraphone-blue/iphone"
	"github.com/user/auraphone-blue/logger"
	"github.com/user/auraphone-blue/phone"
)

// truncateHash safely truncates a hash string to the first n characters
// Returns the full string if it's shorter than n
func truncateHash(hash string, n int) string {
	if len(hash) <= n {
		return hash
	}
	return hash[:n]
}

// PhoneWindow represents a single phone instance with its own window
type PhoneWindow struct {
	window            fyne.Window
	currentTab        string
	app               fyne.App
	hardwareUUID      string                            // Hardware UUID for this phone (for cleanup)
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
	deviceImages      map[string]image.Image // Photo hash -> profile image cache
	devicePhotoHashes map[string]string      // Device ID -> photo hash mapping
	deviceFirstNames  map[string]string      // Device ID -> first_name mapping
}

// NewPhoneWindow creates a new phone window
func NewPhoneWindow(app fyne.App, platformType string) *PhoneWindow {
	// Allocate next hardware UUID from the pool
	manager := GetHardwareUUIDManager()
	hardwareUUID, err := manager.AllocateNextUUID()
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		return nil
	}

	// Select photo that matches hardware UUID index (face1 for first UUID, face2 for second, etc.)
	// This ensures each phone gets a consistent photo across restarts
	allocatedCount := manager.GetAllocatedCount()
	selectedPhoto := fmt.Sprintf("testdata/face%d.jpg", allocatedCount)

	pw := &PhoneWindow{
		currentTab:        "home",
		app:               app,
		hardwareUUID:      hardwareUUID,
		devicesMap:        make(map[string]phone.DiscoveredDevice),
		discoveredDevices: []phone.DiscoveredDevice{},
		needsRefresh:      false,
		selectedPhoto:     selectedPhoto,
		deviceImages:      make(map[string]image.Image),
		devicePhotoHashes: make(map[string]string),
		deviceFirstNames:  make(map[string]string),
	}

	// Create platform-specific phone with hardware UUID
	// NewIPhone/NewAndroid will load/generate DeviceID and firstName internally
	if platformType == "iOS" {
		pw.phone = iphone.NewIPhone(hardwareUUID)
	} else if platformType == "Android" {
		pw.phone = android.NewAndroid(hardwareUUID)
	} else {
		fmt.Printf("Unknown platform type: %s\n", platformType)
		return nil
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

	// Cleanup on close - release hardware UUID back to the pool
	pw.window.SetOnClosed(func() {
		pw.cleanup()
		manager := GetHardwareUUIDManager()
		manager.ReleaseUUID(pw.hardwareUUID)
	})

	// Set initial profile photo
	if err := pw.phone.SetProfilePhoto(pw.selectedPhoto); err != nil {
		fmt.Printf("Failed to set initial profile photo: %v\n", err)
	}

	// Generate and set random profile data for simulation (GUI only)
	// Real phones would have users enter this themselves
	profileData, err := GetProfileForIndex(allocatedCount)
	if err != nil {
		fmt.Printf("Warning: Failed to load profile data: %v (using defaults)\n", err)
		profileData = ProfileData{
			FirstName: platformType,
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
	if err := pw.phone.UpdateLocalProfile(profile); err != nil {
		fmt.Printf("Failed to set initial profile: %v\n", err)
	}

	// Start BLE operations
	pw.phone.Start()

	// Start periodic UI refresh ticker
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for range ticker.C {
			if pw.needsRefresh && pw.currentTab == "home" && pw.deviceListWidget != nil {
				prefix := fmt.Sprintf("%s %s", pw.phone.GetDeviceUUID()[:8], pw.phone.GetPlatform())
				logger.Debug(prefix, "🔄 Ticker triggered GUI refresh (needsRefresh was true)")

				pw.devicesMutex.Lock()
				deviceCount := len(pw.devicesMap)
				hashCount := len(pw.devicePhotoHashes)
				imageCount := len(pw.deviceImages)
				pw.needsRefresh = false
				pw.devicesMutex.Unlock()

				logger.Debug(prefix, "   📊 Refreshing with: %d devices, %d hashes, %d images",
					deviceCount, hashCount, imageCount)

				// Use fyne.Do to ensure thread-safe UI updates
				fyne.Do(func() {
					if pw.deviceListWidget != nil {
						pw.deviceListWidget.Refresh()
						logger.Debug(prefix, "   ✅ deviceListWidget.Refresh() completed")
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

					// Use HardwareUUID as key for lookups (needed by both text and image rendering)
					key := device.HardwareUUID
					if key == "" {
						key = device.DeviceID
					}

					// Border container structure: [center, top, bottom, left, right]
					// Center = textStack, Left = profileStack
					var textStack *fyne.Container
					var profileStack *fyne.Container

					// Find the text stack and profile stack
					for _, child := range row.Objects {
						if child == nil {
							continue
						}
						if container, ok := child.(*fyne.Container); ok {
							// Check if this is the padded profile container (has circle/image)
							if len(container.Objects) > 0 {
								if innerContainer, ok := container.Objects[0].(*fyne.Container); ok {
									// Check if it's a Max container with circle and image
									if len(innerContainer.Objects) == 2 {
										if _, isCircle := innerContainer.Objects[0].(*canvas.Circle); isCircle {
											profileStack = innerContainer
											continue
										}
									}
								}
								// Check if this is the text stack (has text objects)
								if len(container.Objects) >= 3 {
									if _, isText := container.Objects[0].(*canvas.Text); isText {
										textStack = container
									}
								}
							}
						}
					}

					if textStack != nil && len(textStack.Objects) >= 3 {
						nameText := textStack.Objects[0].(*canvas.Text)
						deviceIDText := textStack.Objects[1].(*canvas.Text)
						rssiText := textStack.Objects[2].(*canvas.Text)

						// Use first_name from cache if available, otherwise use device name
						displayName := device.Name
						if firstName, hasName := pw.deviceFirstNames[key]; hasName && firstName != "" {
							displayName = firstName
						}

						// Set name with cyan color (matching iOS)
						nameText.Text = displayName
						nameText.Refresh()

						// Set device info on separate lines
						// Show deviceID if available, otherwise show hardware UUID
						deviceLabel := truncateHash(device.DeviceID, 8)
						if deviceLabel == "" {
							deviceLabel = truncateHash(device.HardwareUUID, 8)
						}
						deviceIDText.Text = fmt.Sprintf("Device: %s", deviceLabel)
						deviceIDText.Refresh()

						rssiText.Text = fmt.Sprintf("RSSI: %.0f dBm, Connected: Yes", device.RSSI)
						rssiText.Refresh()
					}

					// Update profile image if available
					if profileStack != nil && len(profileStack.Objects) >= 2 {
						profileImage := profileStack.Objects[1].(*canvas.Image)
						prefix := fmt.Sprintf("%s %s", pw.phone.GetDeviceUUID()[:8], pw.phone.GetPlatform())

						// Look up device's photo hash using hardware UUID as key, then find image by hash
						if photoHash, hasHash := pw.devicePhotoHashes[key]; hasHash {
							if img, hasImage := pw.deviceImages[photoHash]; hasImage {
								logger.Debug(prefix, "🖼️  Rendering: key=%s, photoHash=%s → ✅ FOUND (imagePtr=%p)",
									truncateHash(key, 8), truncateHash(photoHash, 8), img)
								profileImage.Image = img
							} else {
								// Hash exists but image not loaded yet - clear any stale image from widget reuse
								logger.Warn(prefix, "🖼️  Rendering: key=%s, photoHash=%s → ❌ HASH FOUND but NO IMAGE in deviceImages map",
									truncateHash(key, 8), truncateHash(photoHash, 8))
								logger.Debug(prefix, "   📊 Current deviceImages map has %d entries", len(pw.deviceImages))
								profileImage.Image = nil
							}
						} else {
							// No hash for this device yet - clear any stale image from widget reuse
							logger.Debug(prefix, "🖼️  Rendering: key=%s → ⚠️  NO HASH in devicePhotoHashes map (device=%s)",
								truncateHash(key, 8), device.Name)
							logger.Debug(prefix, "   📊 Current devicePhotoHashes map has %d entries", len(pw.devicePhotoHashes))
							profileImage.Image = nil
						}
						profileImage.Refresh()
					}
				}
			},
		)

		// Add OnSelected handler to show modal when row is tapped
		pw.deviceListWidget.OnSelected = func(id widget.ListItemID) {
			pw.devicesMutex.RLock()
			if id >= len(pw.discoveredDevices) {
				pw.devicesMutex.RUnlock()
				return
			}
			device := pw.discoveredDevices[id]
			pw.devicesMutex.RUnlock()

			// Show person modal
			pw.showPersonModal(device)

			// Deselect the row
			pw.deviceListWidget.UnselectAll()
		}

		// Always return the list widget, even if empty
		// The list will handle its own empty state
		return container.NewMax(bg, pw.deviceListWidget)
	}

	// Profile tab
	if tabName == "profile" {
		return pw.buildProfileTab(bg)
	}

	// Other tabs show placeholder
	label := widget.NewLabelWithStyle(
		fmt.Sprintf("%s View\n(Coming Soon)", tabName),
		fyne.TextAlignCenter,
		fyne.TextStyle{Bold: true},
	)

	return container.NewMax(bg, container.NewCenter(label))
}

// buildProfileTab creates the profile tab content
func (pw *PhoneWindow) buildProfileTab(bg *canvas.Rectangle) fyne.CanvasObject {
	// Load and display the selected profile image
	if pw.profileImage == nil {
		pw.profileImage = canvas.NewImageFromFile(pw.selectedPhoto)
		pw.profileImage.FillMode = canvas.ImageFillContain
	}
	pw.profileImage.File = pw.selectedPhoto
	pw.profileImage.Refresh()

	// Set a fixed size for the profile image
	pw.profileImage.SetMinSize(fyne.NewSize(120, 120))

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

	// Get current profile data
	profile := pw.phone.GetLocalProfileMap()

	// Store initial values for logging
	initialValues := make(map[string]string)
	for k, v := range profile {
		initialValues[k] = v
	}

	// Debug: log what profile values were loaded
	prefix := fmt.Sprintf("%s %s", pw.phone.GetDeviceUUID()[:8], pw.phone.GetPlatform())
	logger.Debug(prefix, "🔍 Profile tab loaded with values:")
	logger.Debug(prefix, "   first_name='%s'", initialValues["first_name"])
	logger.Debug(prefix, "   last_name='%s'", initialValues["last_name"])
	logger.Debug(prefix, "   tagline='%s'", initialValues["tagline"])

	// Create profile form fields with change tracking
	firstNameEntry := widget.NewEntry()
	firstNameEntry.SetPlaceHolder("First Name")
	firstNameEntry.SetText(profile["first_name"])
	firstNameEntry.OnChanged = func(newText string) {
		prefix := fmt.Sprintf("%s %s", pw.phone.GetDeviceUUID()[:8], pw.phone.GetPlatform())
		logger.Debug(prefix, "✏️  first_name OnChanged: '%s' → '%s'", initialValues["first_name"], newText)
	}

	lastNameEntry := widget.NewEntry()
	lastNameEntry.SetPlaceHolder("Last Name")
	lastNameEntry.SetText(profile["last_name"])
	lastNameEntry.OnChanged = func(newText string) {
		prefix := fmt.Sprintf("%s %s", pw.phone.GetDeviceUUID()[:8], pw.phone.GetPlatform())
		logger.Debug(prefix, "✏️  last_name OnChanged: '%s' → '%s'", initialValues["last_name"], newText)
	}

	taglineEntry := widget.NewEntry()
	taglineEntry.SetPlaceHolder("Tagline")
	taglineEntry.SetText(profile["tagline"])
	taglineEntry.OnChanged = func(newText string) {
		prefix := fmt.Sprintf("%s %s", pw.phone.GetDeviceUUID()[:8], pw.phone.GetPlatform())
		logger.Debug(prefix, "✏️  tagline OnChanged: '%s' → '%s'", initialValues["tagline"], newText)
	}

	// Contact method entries
	instaEntry := widget.NewEntry()
	instaEntry.SetPlaceHolder("@username")
	instaEntry.SetText(profile["insta"])

	linkedinEntry := widget.NewEntry()
	linkedinEntry.SetPlaceHolder("linkedin.com/in/username")
	linkedinEntry.SetText(profile["linkedin"])

	youtubeEntry := widget.NewEntry()
	youtubeEntry.SetPlaceHolder("youtube.com/@username")
	youtubeEntry.SetText(profile["youtube"])

	tiktokEntry := widget.NewEntry()
	tiktokEntry.SetPlaceHolder("@username")
	tiktokEntry.SetText(profile["tiktok"])

	gmailEntry := widget.NewEntry()
	gmailEntry.SetPlaceHolder("yourname@gmail.com")
	gmailEntry.SetText(profile["gmail"])

	imessageEntry := widget.NewEntry()
	imessageEntry.SetPlaceHolder("+1 (555) 123-4567")
	imessageEntry.SetText(profile["imessage"])

	whatsappEntry := widget.NewEntry()
	whatsappEntry.SetPlaceHolder("+1 (555) 123-4567")
	whatsappEntry.SetText(profile["whatsapp"])

	signalEntry := widget.NewEntry()
	signalEntry.SetPlaceHolder("+1 (555) 123-4567")
	signalEntry.SetText(profile["signal"])

	telegramEntry := widget.NewEntry()
	telegramEntry.SetPlaceHolder("@username")
	telegramEntry.SetText(profile["telegram"])

	// Define save function to be reused by both button and OnSubmitted handlers
	// This collects current values from ALL fields, logs changes, and saves
	saveProfile := func() {
		prefix := fmt.Sprintf("%s %s", pw.phone.GetDeviceUUID()[:8], pw.phone.GetPlatform())

		// Collect current values from all entry widgets
		currentValues := map[string]string{
			"first_name": firstNameEntry.Text,
			"last_name":  lastNameEntry.Text,
			"tagline":    taglineEntry.Text,
			"insta":      instaEntry.Text,
			"linkedin":   linkedinEntry.Text,
			"youtube":    youtubeEntry.Text,
			"tiktok":     tiktokEntry.Text,
			"gmail":      gmailEntry.Text,
			"imessage":   imessageEntry.Text,
			"whatsapp":   whatsappEntry.Text,
			"signal":     signalEntry.Text,
			"telegram":   telegramEntry.Text,
		}

		// Log all changes
		hasChanges := false
		for fieldName, newValue := range currentValues {
			oldValue := initialValues[fieldName]
			if oldValue != newValue {
				logger.Info(prefix, "📝 Field '%s' changed: '%s' → '%s'", fieldName, oldValue, newValue)
				initialValues[fieldName] = newValue
				hasChanges = true
			}
		}

		if !hasChanges {
			logger.Debug(prefix, "💾 Save triggered but no fields changed")
		}

		// Save to phone
		if err := pw.phone.UpdateLocalProfile(currentValues); err != nil {
			fmt.Printf("Failed to update profile: %v\n", err)
		} else {
			fmt.Printf("Profile updated successfully\n")
		}
	}

	// Simple OnSubmitted handler that just triggers save (which logs all changes)
	onSubmitAnyField := func(string) {
		saveProfile()
	}

	// Add OnSubmitted handlers to all text fields - pressing Return in ANY field saves ALL changes
	firstNameEntry.OnSubmitted = onSubmitAnyField
	lastNameEntry.OnSubmitted = onSubmitAnyField
	taglineEntry.OnSubmitted = onSubmitAnyField
	instaEntry.OnSubmitted = onSubmitAnyField
	linkedinEntry.OnSubmitted = onSubmitAnyField
	youtubeEntry.OnSubmitted = onSubmitAnyField
	tiktokEntry.OnSubmitted = onSubmitAnyField
	gmailEntry.OnSubmitted = onSubmitAnyField
	imessageEntry.OnSubmitted = onSubmitAnyField
	whatsappEntry.OnSubmitted = onSubmitAnyField
	signalEntry.OnSubmitted = onSubmitAnyField
	telegramEntry.OnSubmitted = onSubmitAnyField

	// Save button
	saveButton := widget.NewButton("Save Profile", saveProfile)

	// Create scrollable form
	profileForm := container.NewVBox(
		container.NewCenter(pw.profileImage),
		widget.NewLabel(""),
		widget.NewLabelWithStyle("Profile Photo", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		container.NewPadded(photoSelect),
		widget.NewSeparator(),
		widget.NewLabel(""),
		widget.NewLabelWithStyle("Profile Info", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewForm(
			widget.NewFormItem("First Name", firstNameEntry),
			widget.NewFormItem("Last Name", lastNameEntry),
			widget.NewFormItem("Tagline", taglineEntry),
		),
		widget.NewLabel(""),
		widget.NewLabelWithStyle("Public Contact Methods", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewForm(
			widget.NewFormItem("Instagram", instaEntry),
			widget.NewFormItem("LinkedIn", linkedinEntry),
			widget.NewFormItem("YouTube", youtubeEntry),
			widget.NewFormItem("TikTok", tiktokEntry),
		),
		widget.NewLabel(""),
		widget.NewLabelWithStyle("Private Contact Methods", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewForm(
			widget.NewFormItem("Gmail", gmailEntry),
			widget.NewFormItem("iMessage", imessageEntry),
			widget.NewFormItem("WhatsApp", whatsappEntry),
			widget.NewFormItem("Signal", signalEntry),
			widget.NewFormItem("Telegram", telegramEntry),
		),
		widget.NewLabel(""),
		saveButton,
	)

	scrollable := container.NewVScroll(profileForm)

	return container.NewMax(bg, scrollable)
}

// Show displays the phone window
func (pw *PhoneWindow) Show() {
	pw.window.Show()
}

// onDeviceDiscovered is called when a device is discovered via BLE
func (pw *PhoneWindow) onDeviceDiscovered(device phone.DiscoveredDevice) {
	pw.devicesMutex.Lock()
	defer pw.devicesMutex.Unlock()

	prefix := fmt.Sprintf("%s %s", pw.phone.GetDeviceUUID()[:8], pw.phone.GetPlatform())

	// Safe device ID display (may be empty on initial discovery)
	deviceIDDisplay := "(none)"
	if device.DeviceID != "" {
		deviceIDDisplay = truncateHash(device.DeviceID, 8)
	}

	logger.Debug(prefix, "📱 [GUI CALLBACK] onDeviceDiscovered: deviceID=%s, hardwareUUID=%s, name=%s, photoHash=%s, photoDataLen=%d",
		deviceIDDisplay, device.HardwareUUID[:8], device.Name, truncateHash(device.PhotoHash, 8), len(device.PhotoData))

	// Log current state BEFORE processing
	logger.Debug(prefix, "   📊 BEFORE: %d devices, %d hashes, %d images in memory",
		len(pw.devicesMap), len(pw.devicePhotoHashes), len(pw.deviceImages))

	// Use HardwareUUID as the primary key for deduplication
	// This ensures the same device doesn't appear twice (once with hardware UUID, once with logical deviceID)
	key := device.HardwareUUID
	if key == "" {
		// Fallback to deviceID if hardware UUID not available (shouldn't happen)
		key = device.DeviceID
	}

	logger.Debug(prefix, "   🔑 Using key: %s (from hardwareUUID=%s, deviceID=%s)",
		truncateHash(key, 8), truncateHash(device.HardwareUUID, 8), deviceIDDisplay)

	// Add or update device in map (deduplicates by hardware UUID)
	pw.devicesMap[key] = device

	// If device has photo data (actually received via BLE), load it into memory
	if device.PhotoData != nil && len(device.PhotoData) > 0 {
		logger.Debug(prefix, "   ✅ Photo data present (%d bytes), decoding image...", len(device.PhotoData))

		// Store hash mapping FIRST
		pw.devicePhotoHashes[key] = device.PhotoHash
		logger.Debug(prefix, "   📝 Stored hash mapping: devicePhotoHashes[%s] = %s",
			truncateHash(key, 8), truncateHash(device.PhotoHash, 8))

		// Decode the photo data directly from the callback
		img, _, err := image.Decode(bytes.NewReader(device.PhotoData))
		if err == nil {
			pw.deviceImages[device.PhotoHash] = img
			logger.Info(prefix, "✅ Photo stored in memory: key=%s → photoHash=%s → imagePtr=%p",
				truncateHash(key, 8), truncateHash(device.PhotoHash, 8), img)
			logger.Info(prefix, "📊 AFTER: %d devices, %d hashes, %d images in memory",
				len(pw.devicesMap), len(pw.devicePhotoHashes), len(pw.deviceImages))
			// Dump all mappings for debugging
			logger.Debug(prefix, "   🗺️  All hash mappings:")
			for k, hash := range pw.devicePhotoHashes {
				imgStatus := "❌ NO IMAGE"
				if _, hasImg := pw.deviceImages[hash]; hasImg {
					imgStatus = "✅ HAS IMAGE"
				}
				logger.Debug(prefix, "      %s → %s (%s)", truncateHash(k, 8), truncateHash(hash, 8), imgStatus)
			}
		} else {
			logger.Error(prefix, "❌ Failed to decode photo from %s: %v", truncateHash(key, 8), err)
		}
	} else if device.PhotoHash != "" {
		// Device advertises a photo hash, but we haven't received it yet
		logger.Debug(prefix, "   ⏳ Photo hash present (%s) but no data, trying to load from cache...",
			truncateHash(device.PhotoHash, 8))
		// Try to load from disk cache (from previous session)
		// IMPORTANT: Only store hash mapping if we successfully load the image
		if pw.loadDevicePhoto(device.PhotoHash) {
			pw.devicePhotoHashes[key] = device.PhotoHash
			logger.Debug(prefix, "   📝 Stored hash mapping after cache load: devicePhotoHashes[%s] = %s",
				truncateHash(key, 8), truncateHash(device.PhotoHash, 8))
		} else {
			logger.Debug(prefix, "   ⏸️  NOT storing hash mapping yet (photo not in cache, will arrive via BLE)")
		}
	} else {
		logger.Debug(prefix, "   ⚠️  No photo hash or data for %s", truncateHash(key, 8))
	}

	// Load first_name from device metadata cache (only if we have a deviceID)
	if device.DeviceID != "" {
		cacheManager := phone.NewDeviceCacheManager(pw.phone.GetDeviceUUID())
		if metadata, err := cacheManager.LoadDeviceMetadata(device.DeviceID); err == nil && metadata != nil {
			if metadata.FirstName != "" {
				pw.deviceFirstNames[key] = metadata.FirstName
			}
		}
	}

	// Rebuild sorted list from map
	pw.discoveredDevices = make([]phone.DiscoveredDevice, 0, len(pw.devicesMap))
	for _, dev := range pw.devicesMap {
		pw.discoveredDevices = append(pw.discoveredDevices, dev)
	}

	pw.sortAndRefreshDevices()
}

// loadDevicePhoto loads a cached photo by hash from disk
// Returns true if photo was successfully loaded, false otherwise
func (pw *PhoneWindow) loadDevicePhoto(photoHash string) bool {
	prefix := fmt.Sprintf("%s %s", pw.phone.GetDeviceUUID()[:8], pw.phone.GetPlatform())
	logger.Debug(prefix, "📸 loadDevicePhoto: hash=%s", truncateHash(photoHash, 8))

	// Check if we already have this image cached in memory
	if _, exists := pw.deviceImages[photoHash]; exists {
		logger.Debug(prefix, "   └─ Photo already in memory cache")
		return true // Already loaded, success!
	}

	// Try to load from disk cache using photo hash as filename
	cachePath := filepath.Join(phone.GetDeviceCacheDir(pw.phone.GetDeviceUUID()), "photos", photoHash+".jpg")
	logger.Debug(prefix, "   └─ Attempting to load from: %s", cachePath)

	// Check if file exists first
	if stat, err := os.Stat(cachePath); err != nil {
		if os.IsNotExist(err) {
			logger.Debug(prefix, "   └─ Photo not in cache yet (will be received via BLE)")
		} else {
			logger.Error(prefix, "   └─ Error checking cache file: %v", err)
		}
		return false
	} else {
		logger.Debug(prefix, "   └─ Cache file exists: %d bytes", stat.Size())
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		logger.Error(prefix, "❌ Failed to read cache file: %v", err)
		// Photo not in cache yet - it will be received via BLE photo transfer
		return false
	}

	logger.Debug(prefix, "   └─ Read %d bytes from cache, verifying hash...", len(data))

	// Verify hash matches (content-addressed storage check)
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	if hashStr != photoHash {
		// Cached photo is corrupted, delete it
		os.Remove(cachePath)
		logger.Warn(prefix, "⚠️  Cached photo %s has wrong hash (expected %s, got %s), deleted",
			truncateHash(photoHash, 8), truncateHash(photoHash, 8), truncateHash(hashStr, 8))
		return false
	}

	logger.Debug(prefix, "   └─ Hash verified, decoding image...")

	// Load image into memory
	img, _, err := image.Decode(bytes.NewReader(data))
	if err == nil {
		pw.deviceImages[photoHash] = img
		logger.Info(prefix, "✅ Loaded cached photo from disk: photoHash=%s → imagePtr=%p",
			truncateHash(photoHash, 8), img)
		logger.Info(prefix, "📊 AFTER cache load: %d devices, %d hashes, %d images in memory",
			len(pw.devicesMap), len(pw.devicePhotoHashes), len(pw.deviceImages))

		// Trigger GUI refresh so the photo actually displays
		pw.needsRefresh = true
		logger.Debug(prefix, "   🔄 Set needsRefresh=true to trigger GUI update")
		return true // Success!
	} else {
		logger.Error(prefix, "❌ Failed to decode cached photo %s: %v", truncateHash(photoHash, 8), err)
		return false
	}
}

// sortAndRefreshDevices sorts devices by RSSI and refreshes the UI
func (pw *PhoneWindow) sortAndRefreshDevices() {
	sort.Slice(pw.discoveredDevices, func(i, j int) bool {
		// Primary sort: RSSI (descending)
		if pw.discoveredDevices[i].RSSI != pw.discoveredDevices[j].RSSI {
			return pw.discoveredDevices[i].RSSI > pw.discoveredDevices[j].RSSI
		}
		// Secondary sort: DeviceID (ascending) for stable ordering when RSSI is equal
		return pw.discoveredDevices[i].DeviceID < pw.discoveredDevices[j].DeviceID
	})

	// Mark that we need a refresh (ticker will pick this up)
	pw.needsRefresh = true
}

// showPersonModal displays a modal with detailed person information
func (pw *PhoneWindow) showPersonModal(device phone.DiscoveredDevice) {
	// Load device metadata from cache
	cacheManager := phone.NewDeviceCacheManager(pw.phone.GetDeviceUUID())
	metadata, err := cacheManager.LoadDeviceMetadata(device.DeviceID)

	// Build full name
	displayName := device.Name
	if metadata != nil && metadata.FirstName != "" {
		displayName = metadata.FirstName
		if metadata.LastName != "" {
			displayName += " " + metadata.LastName
		}
	}

	// Create profile image (larger for modal)
	// Use HardwareUUID as key to match how photos are stored in onDeviceDiscovered
	key := device.HardwareUUID
	if key == "" {
		key = device.DeviceID
	}

	var profileImage *canvas.Image
	if photoHash, hasHash := pw.devicePhotoHashes[key]; hasHash {
		if img, hasImage := pw.deviceImages[photoHash]; hasImage {
			profileImage = canvas.NewImageFromImage(img)
			profileImage.FillMode = canvas.ImageFillContain
			profileImage.SetMinSize(fyne.NewSize(120, 120))
		}
	}

	// Fallback to circle if no image
	if profileImage == nil {
		profileImage = canvas.NewImageFromImage(nil)
		profileImage.FillMode = canvas.ImageFillContain
		profileImage.SetMinSize(fyne.NewSize(120, 120))
	}

	// Name label
	nameLabel := widget.NewLabelWithStyle(displayName, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	// Tagline label
	var taglineLabel *widget.Label
	if metadata != nil && metadata.Tagline != "" {
		taglineLabel = widget.NewLabel(metadata.Tagline)
		taglineLabel.Wrapping = fyne.TextWrapWord
		taglineLabel.Alignment = fyne.TextAlignCenter
	}

	// Build contact ways list
	contactWays := container.NewVBox()

	if metadata != nil {
		// Social media section
		if metadata.Insta != "" || metadata.LinkedIn != "" || metadata.YouTube != "" || metadata.TikTok != "" {
			contactWays.Add(widget.NewLabelWithStyle("Social Media", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))

			if metadata.Insta != "" {
				contactWays.Add(widget.NewLabel("Instagram: " + metadata.Insta))
			}
			if metadata.LinkedIn != "" {
				contactWays.Add(widget.NewLabel("LinkedIn: " + metadata.LinkedIn))
			}
			if metadata.YouTube != "" {
				contactWays.Add(widget.NewLabel("YouTube: " + metadata.YouTube))
			}
			if metadata.TikTok != "" {
				contactWays.Add(widget.NewLabel("TikTok: " + metadata.TikTok))
			}
			contactWays.Add(widget.NewLabel(""))
		}

		// Messaging section
		if metadata.Gmail != "" || metadata.IMessage != "" || metadata.WhatsApp != "" || metadata.Signal != "" || metadata.Telegram != "" {
			contactWays.Add(widget.NewLabelWithStyle("Contact Methods", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))

			if metadata.Gmail != "" {
				contactWays.Add(widget.NewLabel("Gmail: " + metadata.Gmail))
			}
			if metadata.IMessage != "" {
				contactWays.Add(widget.NewLabel("iMessage: " + metadata.IMessage))
			}
			if metadata.WhatsApp != "" {
				contactWays.Add(widget.NewLabel("WhatsApp: " + metadata.WhatsApp))
			}
			if metadata.Signal != "" {
				contactWays.Add(widget.NewLabel("Signal: " + metadata.Signal))
			}
			if metadata.Telegram != "" {
				contactWays.Add(widget.NewLabel("Telegram: " + metadata.Telegram))
			}
		}
	}

	// If no contact info available
	if len(contactWays.Objects) == 0 {
		contactWays.Add(widget.NewLabel("No contact information available"))
	}

	// Add device info at bottom
	contactWays.Add(widget.NewLabel(""))
	contactWays.Add(widget.NewSeparator())
	deviceIDLabel := truncateHash(device.DeviceID, 8)
	if deviceIDLabel == "" {
		deviceIDLabel = truncateHash(device.HardwareUUID, 8)
	}
	contactWays.Add(widget.NewLabel(fmt.Sprintf("Device ID: %s", deviceIDLabel)))
	contactWays.Add(widget.NewLabel(fmt.Sprintf("RSSI: %.0f dBm", device.RSSI)))

	// Build modal content
	content := container.NewVBox(
		container.NewCenter(profileImage),
		widget.NewLabel(""),
		nameLabel,
	)

	if taglineLabel != nil {
		content.Add(taglineLabel)
	}

	content.Add(widget.NewLabel(""))
	content.Add(widget.NewSeparator())
	content.Add(widget.NewLabel(""))

	// Add contact ways in scrollable container
	scrollableContacts := container.NewVScroll(contactWays)
	scrollableContacts.SetMinSize(fyne.NewSize(300, 200))
	content.Add(scrollableContacts)

	// Create modal dialog (declare variable first so we can reference it in the close button)
	var dialog *widget.PopUp

	closeButton := widget.NewButton("Close", func() {
		if dialog != nil {
			dialog.Hide()
		}
	})

	dialog = widget.NewModalPopUp(
		container.NewBorder(
			nil,
			closeButton,
			nil, nil,
			container.NewVScroll(content),
		),
		pw.window.Canvas(),
	)

	dialog.Resize(fyne.NewSize(350, 500))
	dialog.Show()

	// Log the action
	if err == nil && metadata != nil {
		fmt.Printf("Showing profile for %s\n", displayName)
	} else {
		fmt.Printf("Showing profile for device %s (limited info)\n", deviceIDLabel)
	}
}

// cleanup cleans up resources when window is closed
func (pw *PhoneWindow) cleanup() {
	fmt.Printf("Closing %s phone (UUID: %s)\n", pw.phone.GetPlatform(), pw.phone.GetDeviceUUID()[:8])
	pw.phone.Stop()
}
