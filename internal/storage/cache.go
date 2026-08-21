package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DiskCache provides helpers to store and retrieve raw GitHub JSON payloads verbatim.
type DiskCache struct {
	BaseDir string
}

// NewDiskCache creates a cache instance rooted at baseDir.
func NewDiskCache(baseDir string) *DiskCache {
	return &DiskCache{BaseDir: baseDir}
}

// PRCachePath returns the path to the cached PR JSON file.
func (c *DiskCache) PRCachePath(repo string, prNumber int) string {
	cleanRepo := strings.ReplaceAll(repo, "/", "_")
	return filepath.Join(c.BaseDir, "github", cleanRepo, "prs", fmt.Sprintf("pr_%d.json", prNumber))
}

// HasPR checks if a PR is already cached.
func (c *DiskCache) HasPR(repo string, prNumber int) bool {
	path := c.PRCachePath(repo, prNumber)
	_, err := os.Stat(path)
	return err == nil
}

// WritePR saves the raw bytes of a PR to the cache.
func (c *DiskCache) WritePR(repo string, prNumber int, rawData []byte) error {
	path := c.PRCachePath(repo, prNumber)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create cache dir: %w", err)
	}
	return os.WriteFile(path, rawData, 0644)
}

// ReadPR reads the raw cached JSON for a PR.
func (c *DiskCache) ReadPR(repo string, prNumber int, target interface{}) error {
	path := c.PRCachePath(repo, prNumber)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read cached PR from %q: %w", path, err)
	}
	return json.Unmarshal(data, target)
}
