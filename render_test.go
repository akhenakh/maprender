package maprender

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchTile_Uncompressed(t *testing.T) {
	// Mock a server that returns a plain byte slice
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mock_tile_data"))
	}))
	defer ts.Close()

	data, err := fetchTile(ts.URL)
	if err != nil {
		t.Fatalf("fetchTile failed: %v", err)
	}
	if string(data) != "mock_tile_data" {
		t.Errorf("expected 'mock_tile_data', got %q", string(data))
	}
}

func TestFetchTile_Gzipped(t *testing.T) {
	// Mock a server that returns a gzipped byte slice
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)

		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		gz.Write([]byte("gzipped_mock_tile_data"))
		gz.Close()

		w.Write(b.Bytes())
	}))
	defer ts.Close()

	data, err := fetchTile(ts.URL)
	if err != nil {
		t.Fatalf("fetchTile failed: %v", err)
	}
	// The fetchTile function handles auto-decompression
	if string(data) != "gzipped_mock_tile_data" {
		t.Errorf("expected 'gzipped_mock_tile_data', got %q", string(data))
	}
}

func TestRender_DimensionsError(t *testing.T) {
	req := RenderRequest{
		Width: 0, Height: 0, // This should trigger an error
	}
	_, err := Render(context.Background(), req)
	if err == nil {
		t.Errorf("expected error when width/height is 0, got nil")
	}
}

func TestRender_Basic(t *testing.T) {
	// Setup a mock tile server returning empty data
	// (which is a valid 0-layer MVT for the decoder)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte{})
	}))
	defer ts.Close()

	style := &MapStyle{
		Layers: []StyleLayer{
			{
				ID:   "background",
				Type: "background",
				Paint: PaintProps{
					BackgroundColor: "#ff0000",
				},
			},
		},
	}

	lat, lng := 40.7128, -74.0060

	req := RenderRequest{
		CenterLat:        lat,
		CenterLng:        lng,
		Zoom:             14,
		Width:            100,
		Height:           100,
		DevicePixelRatio: 1.0,
		Style:            style,
		TileURLTemplate:  ts.URL + "/{z}/{x}/{y}.pbf",
		MarkerLat:        &lat,
		MarkerLng:        &lng,
	}

	img, err := Render(context.Background(), req)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	if img == nil {
		t.Fatalf("Render returned nil image")
	}

	bounds := img.Bounds()
	if bounds.Dx() != 100 || bounds.Dy() != 100 {
		t.Errorf("expected image 100x100, got %dx%d", bounds.Dx(), bounds.Dy())
	}

	// Verify background color painted (red)
	// Because of the center marker, the exact center pixel might be red (marker)
	// or white (marker border). So we test a corner pixel for the background color.
	r, g, b, a := img.At(0, 0).RGBA()

	// color.RGBA{255,0,0,255} becomes uint32 values of 0xffff
	if r != 0xffff || g != 0 || b != 0 || a != 0xffff {
		t.Errorf("expected red background (65535, 0, 0, 65535), got (%d, %d, %d, %d)", r, g, b, a)
	}
}

func TestRender_Cancellation(t *testing.T) {
	// Start with a canceled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := RenderRequest{
		CenterLat: 0, CenterLng: 0, Zoom: 0,
		Width: 100, Height: 100,
		DevicePixelRatio: 1.0,
		Style:            &MapStyle{},
		TileURLTemplate:  "http://dummy",
	}

	_, err := Render(ctx, req)
	if err == nil || err != context.Canceled {
		t.Errorf("expected context.Canceled error, got %v", err)
	}
}

func TestIconTextOffset(t *testing.T) {
	cases := []struct {
		anchor       string
		iconW, iconH float64
		wantDX       float64
		wantDY       float64
	}{
		{"top", 20, 30, 0, 17},
		{"bottom", 20, 30, 0, -17},
		{"left", 20, 30, 12, 0},
		{"right", 20, 30, -12, 0},
		{"center", 20, 30, 0, 0},
		{"", 20, 30, 0, 0},
	}

	for _, tc := range cases {
		dx, dy := iconTextOffset(tc.anchor, tc.iconW, tc.iconH)
		if dx != tc.wantDX || dy != tc.wantDY {
			t.Errorf("iconTextOffset(%q, %v, %v) = (%v, %v); want (%v, %v)",
				tc.anchor, tc.iconW, tc.iconH, dx, dy, tc.wantDX, tc.wantDY)
		}
	}
}

func TestSymbolAngle(t *testing.T) {
	cases := []struct {
		name    string
		angle   float64
		hasIcon bool
		want    float64
	}{
		// A line pointing southwest (e.g. southbound Broadway): text is
		// flipped upright, an icon (one-way arrow) keeps the true direction.
		{"text sw flipped", -135, false, 45},
		{"icon sw keeps direction", -135, true, -135},
		{"text ne unchanged", 45, false, 45},
		{"icon ne unchanged", 45, true, 45},
		{"icon south keeps direction", 135, true, 135},
		{"text south flipped", 135, false, -45},
	}
	for _, c := range cases {
		if got := symbolAngle(c.angle, c.hasIcon); got != c.want {
			t.Errorf("%s: symbolAngle(%v, icon=%v) = %v, want %v",
				c.name, c.angle, c.hasIcon, got, c.want)
		}
	}
}
