package maprender

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"
)

// defaultCacheTTL is the default lifetime of a cached tile (2 weeks).
const defaultCacheTTL = 14 * 24 * time.Hour

// DefaultCacheDir returns the default tile cache directory
// (~/.cache/maprender) based on the current user's home directory.
func DefaultCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "maprender"), nil
}

// TileCache stores downloaded tiles on disk so they can be reused across
// renders and processes. It is safe for concurrent use by multiple processes
// sharing the same directory: tiles are written to a temporary file and then
// atomically moved into place, so a reader never observes a partial tile.
type TileCache struct {
	dir string
	ttl time.Duration
}

// NewTileCache returns a TileCache rooted at dir. When dir is empty the
// default cache directory (~/.cache/maprender) is used. Entries older than ttl
// are treated as missing and re-downloaded; a non-positive ttl disables expiry.
func NewTileCache(dir string, ttl time.Duration) (*TileCache, error) {
	if dir == "" {
		d, err := DefaultCacheDir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &TileCache{dir: dir, ttl: ttl}, nil
}

// pathFor maps a tile URL to its on-disk location. The URL is hashed so the
// cache key is stable regardless of URL length or characters that are
// problematic in file names.
func (c *TileCache) pathFor(url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(c.dir, hex.EncodeToString(sum[:]))
}

// Get returns the cached tile data for url, or ok=false if it is not cached
// or has expired.
func (c *TileCache) Get(url string) ([]byte, bool) {
	path := c.pathFor(url)
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if c.ttl > 0 && time.Since(info.ModTime()) > c.ttl {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// Put stores tile data for url atomically. The data is written to a temporary
// file in the cache directory and then renamed into place, which is atomic on
// the same filesystem and therefore safe when several processes download into
// the same directory concurrently.
func (c *TileCache) Put(url string, data []byte) error {
	path := c.pathFor(url)
	tmp, err := os.CreateTemp(c.dir, ".maprender-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// Fetch returns the tile data for url, using the cache when possible and
// downloading (and caching) it otherwise.
func (c *TileCache) Fetch(url string) ([]byte, error) {
	if data, ok := c.Get(url); ok {
		return data, nil
	}
	data, err := fetchTile(url)
	if err != nil {
		return nil, err
	}
	// Caching is best-effort; a failed write must not fail the fetch.
	if err := c.Put(url, data); err != nil {
		_ = err
	}
	return data, nil
}
