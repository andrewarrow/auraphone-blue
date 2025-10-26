package phone

import (
	"encoding/json"
	"fmt"
	"os"
)

// LocalProfile stores local profile information for a device
// This is shared between iOS and Android implementations
type LocalProfile struct {
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	Tagline        string `json:"tagline"`
	Insta          string `json:"insta"`
	LinkedIn       string `json:"linkedin"`
	YouTube        string `json:"youtube"`
	TikTok         string `json:"tiktok"`
	Gmail          string `json:"gmail"`
	IMessage       string `json:"imessage"`
	WhatsApp       string `json:"whatsapp"`
	Signal         string `json:"signal"`
	Telegram       string `json:"telegram"`
	ProfileVersion int32  `json:"profile_version"` // Increments on any profile change
}

// LoadLocalProfile loads the local profile from disk cache
// Returns a default profile with empty fields if file doesn't exist
func LoadLocalProfile(hardwareUUID string) *LocalProfile {
	cacheDir := GetDeviceCacheDir(hardwareUUID)
	profilePath := fmt.Sprintf("%s/my_profile.json", cacheDir)

	data, err := os.ReadFile(profilePath)
	if err != nil {
		// File doesn't exist yet, return default profile
		return &LocalProfile{
			FirstName:      "",
			LastName:       "",
			Tagline:        "",
			ProfileVersion: 1,
		}
	}

	var profile LocalProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		// Corrupted file, return default
		return &LocalProfile{
			FirstName:      "",
			LastName:       "",
			Tagline:        "",
			ProfileVersion: 1,
		}
	}

	return &profile
}

// SaveLocalProfile saves the local profile to disk cache
func SaveLocalProfile(hardwareUUID string, profile *LocalProfile) error {
	cacheDir := GetDeviceCacheDir(hardwareUUID)
	profilePath := fmt.Sprintf("%s/my_profile.json", cacheDir)

	// Ensure directory exists
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal profile: %w", err)
	}

	// Atomic write: write to temp file, then rename
	tempPath := profilePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp profile: %w", err)
	}

	if err := os.Rename(tempPath, profilePath); err != nil {
		os.Remove(tempPath) // Clean up temp file
		return fmt.Errorf("failed to rename profile file: %w", err)
	}

	return nil
}

// IncrementProfileVersion increments the profile version and saves
// Call this whenever any profile field changes
func IncrementProfileVersion(hardwareUUID string, profile *LocalProfile) error {
	profile.ProfileVersion++
	return SaveLocalProfile(hardwareUUID, profile)
}

// ConvertProfileToMap converts LocalProfile to map[string]string for backwards compatibility
func ConvertProfileToMap(profile *LocalProfile) map[string]string {
	return map[string]string{
		"first_name": profile.FirstName,
		"last_name":  profile.LastName,
		"tagline":    profile.Tagline,
		"insta":      profile.Insta,
		"linkedin":   profile.LinkedIn,
		"youtube":    profile.YouTube,
		"tiktok":     profile.TikTok,
		"gmail":      profile.Gmail,
		"imessage":   profile.IMessage,
		"whatsapp":   profile.WhatsApp,
		"signal":     profile.Signal,
		"telegram":   profile.Telegram,
	}
}

// UpdateProfileFromMap updates LocalProfile from map[string]string for backwards compatibility
func UpdateProfileFromMap(profile *LocalProfile, data map[string]string) {
	if v, ok := data["first_name"]; ok {
		profile.FirstName = v
	}
	if v, ok := data["last_name"]; ok {
		profile.LastName = v
	}
	if v, ok := data["tagline"]; ok {
		profile.Tagline = v
	}
	if v, ok := data["insta"]; ok {
		profile.Insta = v
	}
	if v, ok := data["linkedin"]; ok {
		profile.LinkedIn = v
	}
	if v, ok := data["youtube"]; ok {
		profile.YouTube = v
	}
	if v, ok := data["tiktok"]; ok {
		profile.TikTok = v
	}
	if v, ok := data["gmail"]; ok {
		profile.Gmail = v
	}
	if v, ok := data["imessage"]; ok {
		profile.IMessage = v
	}
	if v, ok := data["whatsapp"]; ok {
		profile.WhatsApp = v
	}
	if v, ok := data["signal"]; ok {
		profile.Signal = v
	}
	if v, ok := data["telegram"]; ok {
		profile.Telegram = v
	}
}
