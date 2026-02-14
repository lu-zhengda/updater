package checker

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/luzhengda/updater/internal/app"
	"github.com/luzhengda/updater/internal/version"
)

// Sparkle RSS/appcast XML structures with proper namespace handling.
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
	Enclosure          sparkleEnclosure `xml:"enclosure"`
}

type sparkleEnclosure struct {
	URL    string `xml:"url,attr"`
	Length string `xml:"length,attr"`
	Type   string `xml:"type,attr"`
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

	item := rss.Channel.Items[0]
	latestVersion := item.ShortVersionString
	if latestVersion == "" {
		latestVersion = item.Version
	}

	return &UpdateResult{
		App:            a,
		Source:         "sparkle",
		CurrentVersion: a.Version,
		LatestVersion:  latestVersion,
		DownloadURL:    item.Enclosure.URL,
		ReleaseNotes:   item.ReleaseNotesLink,
		HasUpdate:      version.IsNewer(a.Version, latestVersion),
	}, nil
}
