package gui

import (
	"fmt"
	"os"
	"strings"
)

// ProfileData holds randomly selected profile information
type ProfileData struct {
	FirstName string
	LastName  string
	Tagline   string
	Instagram string
	YouTube   string
}

var (
	firstNames       []string
	lastNames        []string
	taglines         []string
	instagramHandles []string
	youtubeChannels  []string
)

// LoadProfileData loads profile data from testdata files
func LoadProfileData() error {
	var err error

	firstNames, err = loadLines("testdata/first_names.txt")
	if err != nil {
		return fmt.Errorf("failed to load first_names.txt: %w", err)
	}

	lastNames, err = loadLines("testdata/last_names.txt")
	if err != nil {
		return fmt.Errorf("failed to load last_names.txt: %w", err)
	}

	taglines, err = loadLines("testdata/taglines.txt")
	if err != nil {
		return fmt.Errorf("failed to load taglines.txt: %w", err)
	}

	instagramHandles, err = loadLines("testdata/instagram_handles.txt")
	if err != nil {
		return fmt.Errorf("failed to load instagram_handles.txt: %w", err)
	}

	youtubeChannels, err = loadLines("testdata/youtube_channels.txt")
	if err != nil {
		return fmt.Errorf("failed to load youtube_channels.txt: %w", err)
	}

	return nil
}

// GetProfileForIndex returns profile data for a given index (1-based, cycles through available data)
// This ensures each phone gets consistent profile data based on its index
func GetProfileForIndex(index int) (ProfileData, error) {
	// Ensure profile data is loaded
	if len(firstNames) == 0 {
		if err := LoadProfileData(); err != nil {
			return ProfileData{}, err
		}
	}

	// Convert to 0-based index and cycle through available data
	idx := (index - 1) % len(firstNames)
	lastIdx := (index - 1) % len(lastNames)
	tagIdx := (index - 1) % len(taglines)
	instaIdx := (index - 1) % len(instagramHandles)
	ytIdx := (index - 1) % len(youtubeChannels)

	return ProfileData{
		FirstName: firstNames[idx],
		LastName:  lastNames[lastIdx],
		Tagline:   taglines[tagIdx],
		Instagram: instagramHandles[instaIdx],
		YouTube:   youtubeChannels[ytIdx],
	}, nil
}

// loadLines reads a file and returns non-empty lines
func loadLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	result := []string{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no data found in %s", path)
	}

	return result, nil
}
