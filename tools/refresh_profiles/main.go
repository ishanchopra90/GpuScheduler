package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type hardwareProfile struct {
	Name             string   `json:"name" yaml:"name"`
	MemoryMiB        int      `json:"memoryMiB" yaml:"memoryMiB"`
	MemBandwidthGBps float64  `json:"memBandwidthGBps" yaml:"memBandwidthGBps"`
	PeakTFLOPSBF16   *float64 `json:"peakTFLOPSBF16,omitempty" yaml:"peakTFLOPSBF16,omitempty"`
	InterconnectGBps *float64 `json:"interconnectGBps,omitempty" yaml:"interconnectGBps,omitempty"`
	Notes            string   `json:"notes" yaml:"notes"`
	SourceURL        string   `json:"source_url" yaml:"source_url"`
	SourceURLs       []string `json:"source_urls,omitempty" yaml:"source_urls,omitempty"`
}

type sourceSnapshot struct {
	URL          string    `json:"url"`
	StatusCode   int       `json:"status_code"`
	ContentType  string    `json:"content_type,omitempty"`
	ContentBytes int       `json:"content_bytes"`
	SHA256       string    `json:"sha256"`
	Title        string    `json:"title,omitempty"`
	SavedPath    string    `json:"saved_path"`
	FetchedAt    time.Time `json:"fetched_at"`
}

type profileSnapshot struct {
	Name             string           `json:"name"`
	MemoryMiB        int              `json:"memoryMiB"`
	MemBandwidthGBps float64          `json:"memBandwidthGBps"`
	PeakTFLOPSBF16   *float64         `json:"peakTFLOPSBF16,omitempty"`
	InterconnectGBps *float64         `json:"interconnectGBps,omitempty"`
	SourceURL        string           `json:"source_url"`
	SourceURLs       []string         `json:"source_urls,omitempty"`
	Sources          []sourceSnapshot `json:"sources"`
}

type snapshotFile struct {
	GeneratedAt time.Time         `json:"generated_at"`
	InputYAML   string            `json:"input_yaml"`
	Profiles    []profileSnapshot `json:"profiles"`
}

func main() {
	var (
		inPath          = flag.String("in", "configs/hardware_profiles.yaml", "input YAML path")
		outPath         = flag.String("out", "internal/sim/hardware_profiles.json", "output JSON path")
		check           = flag.Bool("check", false, "verify output is up-to-date without writing")
		fetchSources    = flag.Bool("fetch-sources", false, "fetch official source pages and build snapshot artifacts")
		snapshotOutPath = flag.String("snapshot-out",
			"tools/refresh_profiles/hardware_sources_snapshot.json", "source snapshot JSON output path")
		sourcesDirPath = flag.String("sources-dir", "tools/refresh_profiles/sources",
			"directory to save fetched source pages")
		proposedYAML = flag.String("proposed-yaml-out", "configs/hardware_profiles.review.yaml",
			"manual-review YAML output path")
		timeoutSeconds = flag.Int("http-timeout-seconds", 20, "HTTP timeout for source fetches")
	)
	flag.Parse()

	profiles, err := loadProfiles(*inPath)
	if err != nil {
		fatal(err)
	}
	if err := validateProfiles(profiles); err != nil {
		fatal(err)
	}

	if *fetchSources {
		timeout := time.Duration(*timeoutSeconds) * time.Second
		if err := buildSourceSnapshot(
			profiles, *inPath, *snapshotOutPath, *sourcesDirPath, *proposedYAML, timeout,
		); err != nil {
			fatal(err)
		}
		fmt.Fprintf(os.Stderr, "refresh_profiles: wrote snapshot %s and review YAML %s\n", *snapshotOutPath, *proposedYAML)
	}

	out, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		fatal(fmt.Errorf("marshal json: %w", err))
	}
	out = append(out, '\n')

	if *check {
		existing, err := os.ReadFile(*outPath)
		if err != nil {
			fatal(fmt.Errorf("read output for check: %w", err))
		}
		if !bytes.Equal(existing, out) {
			fatal(errors.New("hardware profiles out of date; run: make refresh-profiles"))
		}
		return
	}

	if err := os.WriteFile(*outPath, out, 0o644); err != nil {
		fatal(fmt.Errorf("write output: %w", err))
	}
}

func buildSourceSnapshot(
	profiles []hardwareProfile, inputYAMLPath, snapshotOutPath, sourcesDirPath, proposedYAMLPath string,
	timeout time.Duration,
) error {
	if err := os.MkdirAll(filepath.Clean(sourcesDirPath), 0o755); err != nil {
		return fmt.Errorf("create sources dir: %w", err)
	}

	client := &http.Client{Timeout: timeout}
	out := snapshotFile{
		GeneratedAt: time.Now().UTC(),
		InputYAML:   inputYAMLPath,
		Profiles:    make([]profileSnapshot, 0, len(profiles)),
	}

	for _, p := range profiles {
		ps := profileSnapshot{
			Name:             p.Name,
			MemoryMiB:        p.MemoryMiB,
			MemBandwidthGBps: p.MemBandwidthGBps,
			PeakTFLOPSBF16:   p.PeakTFLOPSBF16,
			InterconnectGBps: p.InterconnectGBps,
			SourceURL:        p.SourceURL,
			SourceURLs:       dedupeSourceURLs(p),
		}

		for idx, u := range ps.SourceURLs {
			ss, err := fetchAndStoreSource(client, p.Name, idx, u, sourcesDirPath)
			if err != nil {
				return err
			}
			ps.Sources = append(ps.Sources, ss)
		}

		out.Profiles = append(out.Profiles, ps)
	}

	snapshotBytes, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot json: %w", err)
	}
	snapshotBytes = append(snapshotBytes, '\n')
	if err := os.WriteFile(snapshotOutPath, snapshotBytes, 0o644); err != nil {
		return fmt.Errorf("write snapshot json: %w", err)
	}

	// Keep YAML update manual-reviewable by writing to a review file, not replacing source YAML.
	reviewHeader := "# Generated by tools/refresh_profiles with --fetch-sources.\n" +
		"# Manual review required before replacing configs/hardware_profiles.yaml.\n\n"
	reviewYAML, err := yaml.Marshal(profiles)
	if err != nil {
		return fmt.Errorf("marshal review yaml: %w", err)
	}
	if err := os.WriteFile(proposedYAMLPath, append([]byte(reviewHeader), reviewYAML...), 0o644); err != nil {
		return fmt.Errorf("write review yaml: %w", err)
	}
	return nil
}

func fetchAndStoreSource(
	client *http.Client, profileName string, sourceIndex int, rawURL, sourcesDirPath string,
) (sourceSnapshot, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return sourceSnapshot{}, fmt.Errorf("create request for %q: %w", rawURL, err)
	}
	req.Header.Set("User-Agent", "gpu-scheduler-refresh-profiles/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return sourceSnapshot{}, fmt.Errorf("fetch %q: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return sourceSnapshot{}, fmt.Errorf("read response body for %q: %w", rawURL, err)
	}

	filename := buildSourceFilename(profileName, sourceIndex, rawURL, resp.Header.Get("Content-Type"))
	absolutePath := filepath.Join(sourcesDirPath, filename)
	if err := os.WriteFile(absolutePath, body, 0o644); err != nil {
		return sourceSnapshot{}, fmt.Errorf("write source file %q: %w", absolutePath, err)
	}

	sum := sha256.Sum256(body)
	return sourceSnapshot{
		URL:          rawURL,
		StatusCode:   resp.StatusCode,
		ContentType:  resp.Header.Get("Content-Type"),
		ContentBytes: len(body),
		SHA256:       fmt.Sprintf("%x", sum[:]),
		Title:        extractTitle(string(body)),
		SavedPath:    filepath.ToSlash(absolutePath),
		FetchedAt:    time.Now().UTC(),
	}, nil
}

func buildSourceFilename(profileName string, sourceIndex int, rawURL, contentType string) string {
	parsed, _ := url.Parse(rawURL)
	host := strings.ReplaceAll(parsed.Hostname(), ".", "_")
	pathPart := strings.ReplaceAll(strings.Trim(parsed.Path, "/"), "/", "_")
	if pathPart == "" {
		pathPart = "root"
	}
	ext := ".html"
	if strings.Contains(strings.ToLower(contentType), "application/pdf") {
		ext = ".pdf"
	}
	base := fmt.Sprintf("%s-%02d-%s-%s",
		sanitizeToken(profileName), sourceIndex, sanitizeToken(host), sanitizeToken(pathPart))
	return base + ext
}

func sanitizeToken(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "_")
	re := regexp.MustCompile(`[^a-z0-9._-]+`)
	s = re.ReplaceAllString(s, "_")
	s = strings.Trim(s, "._-")
	if s == "" {
		return "unknown"
	}
	return s
}

func extractTitle(content string) string {
	re := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	match := re.FindStringSubmatch(content)
	if len(match) < 2 {
		return ""
	}
	title := strings.TrimSpace(match[1])
	space := regexp.MustCompile(`\s+`)
	return space.ReplaceAllString(title, " ")
}

func dedupeSourceURLs(p hardwareProfile) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 1+len(p.SourceURLs))

	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		if _, exists := seen[u]; exists {
			return
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}

	add(p.SourceURL)
	for _, u := range p.SourceURLs {
		add(u)
	}
	sort.Strings(out)
	return out
}

func loadProfiles(path string) ([]hardwareProfile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read input yaml: %w", err)
	}
	var profiles []hardwareProfile
	if err := yaml.Unmarshal(b, &profiles); err != nil {
		return nil, fmt.Errorf("unmarshal yaml: %w", err)
	}
	return profiles, nil
}

func validateProfiles(profiles []hardwareProfile) error {
	if len(profiles) == 0 {
		return errors.New("no profiles found in YAML")
	}
	seen := map[string]struct{}{}
	for i, p := range profiles {
		key := strings.ToLower(strings.TrimSpace(p.Name))
		if key == "" {
			return fmt.Errorf("profile[%d]: name is required", i)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("profile[%d]: duplicate name %q", i, key)
		}
		seen[key] = struct{}{}

		if p.MemoryMiB <= 0 {
			return fmt.Errorf("profile[%d] %q: memoryMiB must be > 0", i, key)
		}
		if p.MemBandwidthGBps <= 0 {
			return fmt.Errorf("profile[%d] %q: memBandwidthGBps must be > 0", i, key)
		}
		if strings.TrimSpace(p.Notes) == "" {
			return fmt.Errorf("profile[%d] %q: notes is required", i, key)
		}
		if strings.TrimSpace(p.SourceURL) == "" {
			return fmt.Errorf("profile[%d] %q: source_url is required", i, key)
		}
		for j, u := range p.SourceURLs {
			if strings.TrimSpace(u) == "" {
				return fmt.Errorf("profile[%d] %q: source_urls[%d] is empty", i, key, j)
			}
		}
		if _, err := url.ParseRequestURI(strings.TrimSpace(p.SourceURL)); err != nil {
			return fmt.Errorf("profile[%d] %q: source_url is invalid: %w", i, key, err)
		}

		// Keep embedded profile keys canonical.
		profiles[i].Name = key
		profiles[i].SourceURLs = dedupeSourceURLs(p)
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "refresh_profiles: %v\n", err)
	os.Exit(1)
}
