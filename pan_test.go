package maprender

import (
	"context"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"testing"
)

func panTestRequest(tsURL string, lat, lng float64) RenderRequest {
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
	return RenderRequest{
		CenterLat:       lat,
		CenterLng:       lng,
		Zoom:            14,
		Width:           200,
		Height:          200,
		Style:           style,
		TileURLTemplate: tsURL + "/{z}/{x}/{y}.pbf",
	}
}

// lngForLogicalPx converts a horizontal shift in logical pixels to a longitude
// delta (Web Mercator X spans 360 degrees over one world of TileSize pixels).
func lngForLogicalPx(px float64) float64 {
	return px / TileSize * 360
}

func TestRenderIncremental_ReusesPixels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte{})
	}))
	defer ts.Close()

	lat, lng := 40.7128, -74.0060
	req := panTestRequest(ts.URL, lat, lng)

	prev, err := RenderIncremental(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("RenderIncremental failed: %v", err)
	}

	// Pan east by 10 physical pixels.
	const shift = 10
	req.CenterLng += lngForLogicalPx(shift)

	res, err := RenderIncremental(context.Background(), req, prev)
	if err != nil {
		t.Fatalf("RenderIncremental failed: %v", err)
	}
	frame := res.Image

	if frame.Bounds().Dx() != 200 || frame.Bounds().Dy() != 200 {
		t.Fatalf("expected 200x200 image, got %dx%d", frame.Bounds().Dx(), frame.Bounds().Dy())
	}

	// Pixels away from the seams must be copied from the previous base:
	// content moved left by `shift`, so dst(x) == prevBase(x+shift).
	for _, pt := range [][2]int{{100, 100}, {120, 60}, {60, 150}} {
		x, y := pt[0], pt[1]
		got := frame.At(x, y).(color.RGBA)
		want := prev.Base.At(x+shift, y).(color.RGBA)
		if got != want {
			t.Errorf("pixel (%d,%d) = %v; want reused %v", x, y, got, want)
		}
	}

	// The newly exposed strip on the right must have been rendered (opaque
	// red background), not left transparent by the shift.
	c := frame.At(196, 100).(color.RGBA)
	if c.A != 0xff || c.R != 0xff || c.G != 0 || c.B != 0 {
		t.Errorf("exposed strip pixel = %v; want opaque red background", c)
	}
}

func TestRenderIncremental_VerticalPan(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte{})
	}))
	defer ts.Close()

	lat, lng := 40.7128, -74.0060
	req := panTestRequest(ts.URL, lat, lng)

	prev, err := RenderIncremental(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("RenderIncremental failed: %v", err)
	}

	const shift = 8
	// Approximate latitude delta; precise reuse only needs consistent shifts.
	req.CenterLat -= shift / TileSize * 360

	res, err := RenderIncremental(context.Background(), req, prev)
	if err != nil {
		t.Fatalf("RenderIncremental failed: %v", err)
	}

	// Panning south moves content down: dst(x,y) == prevBase(x, y-shift).
	got := res.Image.At(100, 120).(color.RGBA)
	want := prev.Base.At(100, 120-shift).(color.RGBA)
	if got != want {
		t.Errorf("pixel (100,120) = %v; want reused %v", got, want)
	}

	// Newly exposed top strip must be rendered.
	c := res.Image.At(100, 2).(color.RGBA)
	if c.A != 0xff {
		t.Errorf("exposed strip pixel alpha = %d; want 255", c.A)
	}
}

func TestRenderIncremental_Fallbacks(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte{})
	}))
	defer ts.Close()

	ctx := context.Background()
	lat, lng := 40.7128, -74.0060
	req := panTestRequest(ts.URL, lat, lng)

	t.Run("nil prev renders fully", func(t *testing.T) {
		res, err := RenderIncremental(ctx, req, nil)
		if err != nil {
			t.Fatalf("RenderIncremental failed: %v", err)
		}
		if res.Image.Bounds().Dx() != 200 || res.Image.Bounds().Dy() != 200 {
			t.Errorf("expected 200x200, got %dx%d", res.Image.Bounds().Dx(), res.Image.Bounds().Dy())
		}
	})

	t.Run("size mismatch falls back to full render", func(t *testing.T) {
		prev, err := RenderIncremental(ctx, req, nil)
		if err != nil {
			t.Fatal(err)
		}
		small := &PanFrame{Base: prev.Base.SubImage(image.Rect(0, 0, 100, 100)).(*image.RGBA), Zoom: req.Zoom}
		req.CenterLng += lngForLogicalPx(10)
		res, err := RenderIncremental(ctx, req, small)
		if err != nil {
			t.Fatalf("RenderIncremental failed: %v", err)
		}
		if res.Image.Bounds().Dx() != 200 || res.Image.Bounds().Dy() != 200 {
			t.Errorf("expected full-render 200x200, got %dx%d", res.Image.Bounds().Dx(), res.Image.Bounds().Dy())
		}
	})

	t.Run("zero shift reuses previous base", func(t *testing.T) {
		prev, err := RenderIncremental(ctx, req, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := RenderIncremental(ctx, req, prev)
		if err != nil {
			t.Fatalf("RenderIncremental failed: %v", err)
		}
		if res.Image.Bounds().Dx() != 200 || res.Image.Bounds().Dy() != 200 {
			t.Errorf("expected fresh 200x200 render, got %dx%d", res.Image.Bounds().Dx(), res.Image.Bounds().Dy())
		}
	})

	t.Run("huge shift falls back to full render", func(t *testing.T) {
		prev, err := RenderIncremental(ctx, req, nil)
		if err != nil {
			t.Fatal(err)
		}
		far := panTestRequest(ts.URL, lat, lng+lngForLogicalPx(500))
		res, err := RenderIncremental(ctx, far, prev)
		if err != nil {
			t.Fatalf("RenderIncremental failed: %v", err)
		}
		if res.Image.Bounds().Dx() != 200 || res.Image.Bounds().Dy() != 200 {
			t.Errorf("expected 200x200, got %dx%d", res.Image.Bounds().Dx(), res.Image.Bounds().Dy())
		}
	})
}

func assertPixelsEqual(t *testing.T, dpr float64, w, h int, inc, full *image.RGBA) {
	t.Helper()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			ci := inc.RGBAAt(x, y)
			if ci.A != 0xff {
				t.Fatalf("dpr %v: transparent pixel at (%d,%d): %v", dpr, x, y, ci)
			}
			cf := full.RGBAAt(x, y)
			if ci != cf {
				t.Fatalf("dpr %v: pixel (%d,%d) = %v; want %v from full render", dpr, x, y, ci, cf)
			}
		}
	}
}

// Regression test: patches were rendered with logical instead of physical
// dimensions, producing misaligned and transparent (dark) regions whenever
// DevicePixelRatio > 1. The incremental result must be pixel-identical to a
// full render of the panned view.
func TestRenderIncremental_HiDPIMatchesFullRender(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte{})
	}))
	defer ts.Close()

	const w, h, dpr = 400, 300, 2
	style := &MapStyle{
		Layers: []StyleLayer{
			{ID: "background", Type: "background", Paint: PaintProps{BackgroundColor: "#ff0000"}},
		},
	}
	mkReq := func(lat, lng float64) RenderRequest {
		return RenderRequest{
			CenterLat: lat, CenterLng: lng, Zoom: 17,
			Width: w, Height: h, DevicePixelRatio: dpr,
			Style:           style,
			TileURLTemplate: ts.URL + "/{z}/{x}/{y}.pbf",
		}
	}

	lat, lng := 48.86, 2.34
	prev, err := RenderIncremental(context.Background(), mkReq(lat, lng), nil)
	if err != nil {
		t.Fatalf("RenderIncremental failed: %v", err)
	}

	// Diagonal pan by ~30 physical pixels.
	panned := mkReq(lat+lngForLogicalPx(15), lng+lngForLogicalPx(30))

	inc, err := RenderIncremental(context.Background(), panned, prev)
	if err != nil {
		t.Fatalf("RenderIncremental failed: %v", err)
	}
	full, err := Render(context.Background(), panned)
	if err != nil {
		t.Fatalf("full Render failed: %v", err)
	}

	assertPixelsEqual(t, dpr, w, h, inc.Image, full)
}

// TestRenderIncremental_LabelsMatchFullRender verifies two things:
//   - the full-frame symbol pass: text labels are viewport-dependent
//     (collision detection), so they must be rendered once per pan over the
//     whole composited frame, never per patch;
//   - labels never stack across pans: every incremental frame must be
//     pixel-identical to a full render even after several consecutive pans.
func TestRenderIncremental_LabelsMatchFullRender(t *testing.T) {
	// Grid of labeled POIs so labels land inside the viewport at any DPR.
	var pts [][2]int32
	for gy := int32(500); gy < 4096; gy += 800 {
		for gx := int32(500); gx < 4096; gx += 800 {
			pts = append(pts, [2]int32{gx, gy})
		}
	}
	tile := encodeMVTPointTile("poi", 4096, pts, "name", "Hello")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tile)
	}))
	defer ts.Close()

	style := &MapStyle{
		Layers: []StyleLayer{
			{ID: "background", Type: "background", Paint: PaintProps{BackgroundColor: "#ff0000"}},
			{
				ID:          "label",
				Type:        "symbol",
				SourceLayer: "poi",
				Layout:      LayoutProps{TextField: "Hello", TextSize: 18},
				Paint:       PaintProps{TextColor: "#000000"},
			},
		},
	}

	for _, dpr := range []float64{1, 2} {
		const w, h = 400, 300
		mkReq := func(lat, lng float64) RenderRequest {
			return RenderRequest{
				CenterLat: lat, CenterLng: lng, Zoom: 14,
				Width: w, Height: h, DevicePixelRatio: dpr,
				Style:           style,
				TileURLTemplate: ts.URL + "/{z}/{x}/{y}.pbf",
			}
		}

		lat, lng := 40.7128, -74.0060

		// Sanity: the label is actually drawn (dark pixels on red background).
		first, err := RenderIncremental(context.Background(), mkReq(lat, lng), nil)
		if err != nil {
			t.Fatalf("dpr %v: RenderIncremental failed: %v", dpr, err)
		}
		dark := 0
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if c := first.Image.RGBAAt(x, y); c.R < 0x80 && c.G < 0x80 && c.B < 0x80 {
					dark++
				}
			}
		}
		if dark == 0 {
			t.Fatalf("dpr %v: no label pixels found in reference render", dpr)
		}

		// Chain several small pans; each frame must match a full render.
		prev := first
		for i := 1; i <= 3; i++ {
			// ~13 physical pixels per pan, diagonal.
			lat += lngForLogicalPx(15)
			lng += lngForLogicalPx(30)
			req := mkReq(lat, lng)

			res, err := RenderIncremental(context.Background(), req, prev)
			if err != nil {
				t.Fatalf("dpr %v: RenderIncremental failed: %v", dpr, err)
			}
			full, err := Render(context.Background(), req)
			if err != nil {
				t.Fatalf("dpr %v: full Render failed: %v", dpr, err)
			}
			assertPixelsEqual(t, dpr, w, h, res.Image, full)
			prev = res
		}
	}
}
