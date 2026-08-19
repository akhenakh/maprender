package maprender

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTileCacheRoundtrip(t *testing.T) {
	dir := t.TempDir()
	c, err := NewTileCache(dir, 0)
	if err != nil {
		t.Fatalf("NewTileCache: %v", err)
	}

	if _, ok := c.Get("https://example.com/1/2/3.pbf"); ok {
		t.Fatal("expected cache miss")
	}

	if err := c.Put("https://example.com/1/2/3.pbf", []byte("tile-data")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	data, ok := c.Get("https://example.com/1/2/3.pbf")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(data) != "tile-data" {
		t.Fatalf("expected tile-data, got %q", string(data))
	}

	// A different URL must not collide.
	if _, ok := c.Get("https://example.com/1/2/4.pbf"); ok {
		t.Fatal("expected cache miss for different URL")
	}
}

func TestTileCacheExpiry(t *testing.T) {
	dir := t.TempDir()
	c, err := NewTileCache(dir, time.Hour)
	if err != nil {
		t.Fatalf("NewTileCache: %v", err)
	}

	if err := c.Put("https://example.com/z/x/y.pbf", []byte("stale")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Backdate the file to simulate an expired entry.
	path := c.pathFor("https://example.com/z/x/y.pbf")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	if _, ok := c.Get("https://example.com/z/x/y.pbf"); ok {
		t.Fatal("expected expired entry to be a miss")
	}
}

func TestTileCacheAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	c, err := NewTileCache(dir, 0)
	if err != nil {
		t.Fatalf("NewTileCache: %v", err)
	}

	url := "https://example.com/concurrent.pbf"
	const workers = 32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Put(url, []byte("payload")); err != nil {
				t.Errorf("Put: %v", err)
			}
		}()
	}
	wg.Wait()

	data, ok := c.Get(url)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(data) != "payload" {
		t.Fatalf("expected payload, got %q", string(data))
	}

	// No temporary files should be left behind after the atomic moves.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(c.pathFor(url)) {
			t.Fatalf("unexpected leftover file in cache dir: %s", e.Name())
		}
	}
}
