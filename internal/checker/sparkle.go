package checker

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/version"
)

// Sparkle RSS/appcast XML structures.
// In real-world Sparkle feeds, version info is typically on enclosure attributes,
// not child elements. We support both for maximum compatibility.
type sparkleRSS struct {
	XMLName xml.Name       `xml:"rss"`
	Channel sparkleChannel `xml:"channel"`
}

type sparkleChannel struct {
	Items []sparkleItem `xml:"item"`
}

type sparkleItem struct {
	Title              string           `xml:"title"`
	Version            string           `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle version"`
	ShortVersionString string           `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle shortVersionString"`
	ReleaseNotesLink   string           `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle releaseNotesLink"`
	MinSystemVersion   string           `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle minimumSystemVersion"`
	MaxSystemVersion   string           `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle maximumSystemVersion"`
	Enclosure          sparkleEnclosure `xml:"enclosure"`
}

type sparkleEnclosure struct {
	URL                string `xml:"url,attr"`
	Length             string `xml:"length,attr"`
	Type               string `xml:"type,attr"`
	Version            string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle version,attr"`
	ShortVersionString string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle shortVersionString,attr"`
}

// SparkleChecker checks for updates via Sparkle appcast feeds.
type SparkleChecker struct {
	client *http.Client
}

// NewSparkleChecker creates a new SparkleChecker with the given HTTP client.
// If client is nil, http.DefaultClient is used.
func NewSparkleChecker(client *http.Client) *SparkleChecker {
	if client == nil {
		client = http.DefaultClient
	}
	return &SparkleChecker{client: client}
}

// Name returns the checker's display name.
func (s *SparkleChecker) Name() string {
	return "sparkle"
}

// CanCheck returns true if the app has a Sparkle feed URL.
func (s *SparkleChecker) CanCheck(a *app.App) bool {
	return a.FeedURL != ""
}

// Check fetches the Sparkle appcast feed and compares the latest version.
func (s *SparkleChecker) Check(ctx context.Context, a *app.App) (*UpdateResult, error) {
	if a.FeedURL == "" {
		return nil, fmt.Errorf("failed to check sparkle update: no feed URL for %s", a.Name)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.FeedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s: %w", a.Name, err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch appcast for %s: %w", a.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch appcast for %s: status %d", a.Name, resp.StatusCode)
	}

	var rss sparkleRSS
	if err := xml.NewDecoder(resp.Body).Decode(&rss); err != nil {
		return nil, fmt.Errorf("failed to parse appcast for %s: %w", a.Name, err)
	}

	if len(rss.Channel.Items) == 0 {
		return nil, fmt.Errorf("failed to check sparkle update: no items in appcast for %s", a.Name)
	}

	// Find the best matching item: filter by macOS version, pick the last
	// compatible item (feeds often list oldest first, newest last, or newest first).
	macOSVersion := getMacOSVersionFn()
	item := findBestItem(rss.Channel.Items, macOSVersion)

	// Extract version: prefer enclosure attributes (most common in real feeds),
	// fall back to item child elements.
	latestVersion := item.Enclosure.ShortVersionString
	if latestVersion == "" {
		latestVersion = item.ShortVersionString
	}
	if latestVersion == "" {
		latestVersion = item.Enclosure.Version
	}
	if latestVersion == "" {
		latestVersion = item.Version
	}

	hasUpdate := version.IsNewer(a.Version, latestVersion)

	// Detect stale feed: if the feed's latest is older than installed,
	// the feed is likely abandoned (e.g., PDF Expert Sparkle returns v2.x for v3.x installs).
	stale := !hasUpdate && latestVersion != "" && version.IsNewer(latestVersion, a.Version)

	return &UpdateResult{
		App:            a,
		Source:         "sparkle",
		CurrentVersion: a.Version,
		LatestVersion:  latestVersion,
		DownloadURL:    item.Enclosure.URL,
		ReleaseNotes:   item.ReleaseNotesLink,
		HasUpdate:      hasUpdate,
		IsMajorUpdate:  version.IsMajorUpgrade(a.Version, latestVersion),
		StaleSource:    stale,
	}, nil
}

// findBestItem picks the most appropriate item from the feed,
// filtering by macOS compatibility and preferring the newest version.
func findBestItem(items []sparkleItem, macOSVersion string) sparkleItem {
	var best sparkleItem
	bestVersion := ""

	for _, item := range items {
		// Skip items incompatible with current macOS
		if item.MinSystemVersion != "" && macOSVersion != "" {
			if !version.IsNewerOrEqual(item.MinSystemVersion, macOSVersion) {
				continue
			}
		}
		if item.MaxSystemVersion != "" && macOSVersion != "" {
			if version.IsNewer(item.MaxSystemVersion, macOSVersion) {
				continue
			}
		}

		// Get this item's version
		v := item.Enclosure.ShortVersionString
		if v == "" {
			v = item.ShortVersionString
		}
		if v == "" {
			v = item.Enclosure.Version
		}
		if v == "" {
			v = item.Version
		}

		if bestVersion == "" || version.IsNewer(bestVersion, v) {
			best = item
			bestVersion = v
		}
	}

	// If no compatible item found, return the first one
	if bestVersion == "" && len(items) > 0 {
		return items[0]
	}
	return best
}

// getMacOSVersionFn returns the current macOS version (e.g., "15.3").
// It is a variable so tests can override it.
var getMacOSVersionFn = func() string {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
