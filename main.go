package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
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
	manager := phone.GetHardwareUUIDManager()
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
	if platformType == "iOS" {
		pw.phone = iphone.NewIPhone(hardwareUUID)
	} else {
		pw.phone = android.NewAndroid(hardwareUUID)
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
		manager := phone.GetHardwareUUIDManager()
		manager.ReleaseUUID(pw.hardwareUUID)
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
						// Look up device's photo hash using hardware UUID as key, then find image by hash
						if photoHash, hasHash := pw.devicePhotoHashes[key]; hasHash {
							if img, hasImage := pw.deviceImages[photoHash]; hasImage {
								prefix := fmt.Sprintf("%s %s", pw.phone.GetDeviceUUID()[:8], pw.phone.GetPlatform())
								logger.Debug(prefix, "🖼️  Rendering list item: key=%s, photoHash=%s, imagePtr=%p",
									truncateHash(key, 8), truncateHash(photoHash, 8), img)
								profileImage.Image = img
							} else {
								// Hash exists but image not loaded yet - clear any stale image from widget reuse
								profileImage.Image = nil
							}
						} else {
							// No hash for this device yet - clear any stale image from widget reuse
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

		// Create profile form fields
		firstNameEntry := widget.NewEntry()
		firstNameEntry.SetPlaceHolder("First Name")
		firstNameEntry.SetText(profile["first_name"])

		lastNameEntry := widget.NewEntry()
		lastNameEntry.SetPlaceHolder("Last Name")
		lastNameEntry.SetText(profile["last_name"])

		taglineEntry := widget.NewEntry()
		taglineEntry.SetPlaceHolder("Tagline")
		taglineEntry.SetText(profile["tagline"])

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
		saveProfile := func() {
			updatedProfile := map[string]string{
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
			if err := pw.phone.UpdateLocalProfile(updatedProfile); err != nil {
				fmt.Printf("Failed to update profile: %v\n", err)
			} else {
				fmt.Printf("Profile updated successfully\n")
			}
		}

		// Add OnSubmitted handlers to all text fields to save on Return key
		firstNameEntry.OnSubmitted = func(string) { saveProfile() }
		lastNameEntry.OnSubmitted = func(string) { saveProfile() }
		taglineEntry.OnSubmitted = func(string) { saveProfile() }
		instaEntry.OnSubmitted = func(string) { saveProfile() }
		linkedinEntry.OnSubmitted = func(string) { saveProfile() }
		youtubeEntry.OnSubmitted = func(string) { saveProfile() }
		tiktokEntry.OnSubmitted = func(string) { saveProfile() }
		gmailEntry.OnSubmitted = func(string) { saveProfile() }
		imessageEntry.OnSubmitted = func(string) { saveProfile() }
		whatsappEntry.OnSubmitted = func(string) { saveProfile() }
		signalEntry.OnSubmitted = func(string) { saveProfile() }
		telegramEntry.OnSubmitted = func(string) { saveProfile() }

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

	prefix := fmt.Sprintf("%s %s", pw.phone.GetDeviceUUID()[:8], pw.phone.GetPlatform())

	// Safe device ID display (may be empty on initial discovery)
	deviceIDDisplay := "(none)"
	if device.DeviceID != "" {
		deviceIDDisplay = truncateHash(device.DeviceID, 8)
	}

	logger.Debug(prefix, "📱 [GUI CALLBACK] onDeviceDiscovered: deviceID=%s, hardwareUUID=%s, name=%s, photoHash=%s, photoDataLen=%d",
		deviceIDDisplay, device.HardwareUUID[:8], device.Name, truncateHash(device.PhotoHash, 8), len(device.PhotoData))

	// Use HardwareUUID as the primary key for deduplication
	// This ensures the same device doesn't appear twice (once with hardware UUID, once with logical deviceID)
	key := device.HardwareUUID
	if key == "" {
		// Fallback to deviceID if hardware UUID not available (shouldn't happen)
		key = device.DeviceID
	}

	// Add or update device in map (deduplicates by hardware UUID)
	pw.devicesMap[key] = device

	// If device has photo data (actually received via BLE), load it into memory
	if device.PhotoData != nil && len(device.PhotoData) > 0 {
		pw.devicePhotoHashes[key] = device.PhotoHash
		logger.Debug(prefix, "   └─ Photo data present, decoding image...")
		// Decode the photo data directly from the callback
		img, _, err := image.Decode(bytes.NewReader(device.PhotoData))
		if err == nil {
			pw.deviceImages[device.PhotoHash] = img
			logger.Info(prefix, "📷 Stored photo: key=%s → photoHash=%s → imagePtr=%p",
				truncateHash(key, 8), truncateHash(device.PhotoHash, 8), img)
			logger.Info(prefix, "📊 Current state: %d devices, %d hashes, %d images",
				len(pw.devicesMap), len(pw.devicePhotoHashes), len(pw.deviceImages))
			// Dump all mappings for debugging
			for k, hash := range pw.devicePhotoHashes {
				logger.Debug(prefix, "   └─ Mapping: key=%s → photoHash=%s", truncateHash(k, 8), truncateHash(hash, 8))
			}
		} else {
			logger.Error(prefix, "❌ Failed to decode photo from %s: %v", truncateHash(key, 8), err)
		}
	} else if device.PhotoHash != "" {
		// Device advertises a photo hash, but we haven't received it yet
		pw.devicePhotoHashes[key] = device.PhotoHash
		logger.Debug(prefix, "   └─ Photo hash present but no data, trying to load from cache...")
		// Try to load from disk cache (from previous session)
		pw.loadDevicePhoto(device.PhotoHash)
	} else {
		logger.Debug(prefix, "   └─ No photo hash or data for %s", truncateHash(key, 8))
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
func (pw *PhoneWindow) loadDevicePhoto(photoHash string) {
	prefix := fmt.Sprintf("%s %s", pw.phone.GetDeviceUUID()[:8], pw.phone.GetPlatform())
	logger.Debug(prefix, "📸 loadDevicePhoto: hash=%s", truncateHash(photoHash, 8))

	// Check if we already have this image cached in memory
	if _, exists := pw.deviceImages[photoHash]; exists {
		logger.Debug(prefix, "   └─ Photo already in memory cache")
		return
	}

	// Try to load from disk cache using photo hash as filename
	cachePath := fmt.Sprintf("data/%s/cache/photos/%s.jpg", pw.phone.GetDeviceUUID(), photoHash)
	logger.Debug(prefix, "   └─ Attempting to load from: %s", cachePath)

	// Check if file exists first
	if stat, err := os.Stat(cachePath); err != nil {
		if os.IsNotExist(err) {
			logger.Debug(prefix, "   └─ Photo not in cache yet (will be received via BLE)")
		} else {
			logger.Error(prefix, "   └─ Error checking cache file: %v", err)
		}
		return
	} else {
		logger.Debug(prefix, "   └─ Cache file exists: %d bytes", stat.Size())
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		logger.Error(prefix, "❌ Failed to read cache file: %v", err)
		// Photo not in cache yet - it will be received via BLE photo transfer
		return
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
		return
	}

	logger.Debug(prefix, "   └─ Hash verified, decoding image...")

	// Load image into memory
	img, _, err := image.Decode(bytes.NewReader(data))
	if err == nil {
		pw.deviceImages[photoHash] = img
		prefix := fmt.Sprintf("%s %s", pw.phone.GetDeviceUUID()[:8], pw.phone.GetPlatform())
		logger.Debug(prefix, "📷 Loaded cached photo %s from disk", truncateHash(photoHash, 8))
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

// runStressTest runs 2 iPhones and 2 Android devices in headless mode
func runStressTest() {
	fmt.Println("=== Auraphone Blue - Stress Test Mode ===")
	fmt.Println("Starting 2 iOS and 2 Android devices in headless mode...")

	// Note: Log level is already set from CLI flag in main(), don't override it here

	// Create device manager for hardware UUIDs
	manager := phone.GetHardwareUUIDManager()

	// Create 2 iPhones
	iphones := make([]phone.Phone, 2)
	for i := 0; i < 2; i++ {
		hardwareUUID, err := manager.AllocateNextUUID()
		if err != nil {
			fmt.Printf("ERROR: Failed to allocate UUID for iPhone %d: %v\n", i+1, err)
			return
		}

		iphones[i] = iphone.NewIPhone(hardwareUUID)
		if iphones[i] == nil {
			fmt.Printf("ERROR: Failed to create iPhone %d\n", i+1)
			return
		}

		// Set profile photo (face1 for first iPhone, face2 for second)
		photoPath := fmt.Sprintf("testdata/face%d.jpg", i+1)
		if err := iphones[i].SetProfilePhoto(photoPath); err != nil {
			fmt.Printf("Warning: Failed to set profile photo for iPhone %d: %v\n", i+1, err)
		}

		// Start the phone
		iphones[i].Start()
		fmt.Printf("Started iPhone %d (UUID: %s, Device ID: %s)\n",
			i+1, hardwareUUID[:8], iphones[i].GetDeviceUUID()[:8])
	}

	// Create 2 Android devices
	androids := make([]phone.Phone, 2)
	for i := 0; i < 2; i++ {
		hardwareUUID, err := manager.AllocateNextUUID()
		if err != nil {
			fmt.Printf("ERROR: Failed to allocate UUID for Android %d: %v\n", i+1, err)
			return
		}

		androids[i] = android.NewAndroid(hardwareUUID)
		if androids[i] == nil {
			fmt.Printf("ERROR: Failed to create Android %d\n", i+1)
			return
		}

		// Set profile photo (face3 for first Android, face4 for second)
		photoPath := fmt.Sprintf("testdata/face%d.jpg", i+3)
		if err := androids[i].SetProfilePhoto(photoPath); err != nil {
			fmt.Printf("Warning: Failed to set profile photo for Android %d: %v\n", i+1, err)
		}

		// Start the phone
		androids[i].Start()
		fmt.Printf("Started Android %d (UUID: %s, Device ID: %s)\n",
			i+1, hardwareUUID[:8], androids[i].GetDeviceUUID()[:8])
	}

	fmt.Println("\nAll devices started. Press Ctrl+C to stop...")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println("\nShutting down devices...")

	// Stop all devices
	for i, p := range iphones {
		if p != nil {
			p.Stop()
			fmt.Printf("Stopped iPhone %d\n", i+1)
		}
	}

	for i, p := range androids {
		if p != nil {
			p.Stop()
			fmt.Printf("Stopped Android %d\n", i+1)
		}
	}

	fmt.Println("Stress test completed.")
}

func main() {
	// Parse CLI flags
	stressTest := flag.Bool("stress-test", false, "Run headless stress test with 2 iPhones and 2 Android devices")
	logLevel := flag.String("log-level", "TRACE", "Set log level (ERROR, WARN, INFO, DEBUG, TRACE)")
	flag.Parse()

	fmt.Println("=== Auraphone Blue ===")

	// Set log level from CLI flag
	logger.SetLevel(logger.ParseLevel(*logLevel))
	fmt.Printf("Log level set to: %s\n", *logLevel)

	// Clean up old device directories from previous runs
	if err := cleanupOldDevices(); err != nil {
		fmt.Printf("Warning: failed to cleanup old devices: %v\n", err)
	}

	// Run in stress test mode if flag is provided
	if *stressTest {
		runStressTest()
		return
	}

	// Otherwise, run the normal GUI launcher
	fmt.Println("Starting launcher menu...")
	launcher := NewLauncher(*logLevel)
	launcher.Run()
}
