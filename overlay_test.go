package maprender

import (
	"context"
	"image/color"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOverlayFromGeoJSON(t *testing.T) {
	t.Run("Feature", func(t *testing.T) {
		data := []byte(`{"type":"Feature","properties":{"stroke":"#00ff00","fill":"#0000ff"},"geometry":{"type":"Point","coordinates":[2.3,48.8]}}`)
		overlays, err := OverlayFromGeoJSON(data)
		if err != nil {
			t.Fatalf("OverlayFromGeoJSON: %v", err)
		}
		if len(overlays) != 1 {
			t.Fatalf("len = %d; want 1", len(overlays))
		}
		if !overlays[0].Geometry.IsPoint() {
			t.Errorf("expected point geometry")
		}
		if _, ok := overlays[0].Properties["stroke"]; !ok {
			t.Errorf("expected stroke property")
		}
	})

	t.Run("FeatureCollection", func(t *testing.T) {
		data := []byte(`{"type":"FeatureCollection","features":[{"type":"Feature","properties":{},"geometry":{"type":"Point","coordinates":[2.3,48.8]}},{"type":"Feature","properties":{},"geometry":{"type":"Point","coordinates":[2.4,48.9]}}]}`)
		overlays, err := OverlayFromGeoJSON(data)
		if err != nil {
			t.Fatalf("OverlayFromGeoJSON: %v", err)
		}
		if len(overlays) != 2 {
			t.Fatalf("len = %d; want 2", len(overlays))
		}
	})

	t.Run("Geometry", func(t *testing.T) {
		data := []byte(`{"type":"Polygon","coordinates":[[[2.3,48.8],[2.4,48.8],[2.4,48.9],[2.3,48.8]]]}`)
		overlays, err := OverlayFromGeoJSON(data)
		if err != nil {
			t.Fatalf("OverlayFromGeoJSON: %v", err)
		}
		if len(overlays) != 1 || !overlays[0].Geometry.IsPolygon() {
			t.Errorf("expected polygon geometry")
		}
	})
}

func TestOverlayFromWKTAndWKB(t *testing.T) {
	o, err := OverlayFromWKT("POINT(2.3 48.8)")
	if err != nil {
		t.Fatalf("OverlayFromWKT: %v", err)
	}
	if !o.Geometry.IsPoint() {
		t.Errorf("expected point")
	}

	wkb := o.Geometry.AsBinary()
	o2, err := OverlayFromWKB(wkb)
	if err != nil {
		t.Fatalf("OverlayFromWKB: %v", err)
	}
	if !o2.Geometry.IsPoint() {
		t.Errorf("expected point from WKB")
	}
}

func TestOverlayColors(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	blue := color.RGBA{0, 0, 255, 255}

	o := Overlay{}
	if got := o.strokeColor(); got != red {
		t.Errorf("default stroke = %v; want red", got)
	}
	if o.fillColor() != nil {
		t.Errorf("default fill should be nil (transparent)")
	}

	o = Overlay{StrokeColor: blue, FillColor: red}
	if o.strokeColor() != blue || o.fillColor() != red {
		t.Errorf("explicit colors not respected")
	}

	o = Overlay{Properties: map[string]any{"stroke-color": "#00ff00", "fill": "#ff0000"}}
	if got := o.strokeColor(); got != (color.RGBA{0, 255, 0, 255}) {
		t.Errorf("property stroke = %v; want green", got)
	}
	if got := o.fillColor(); got != red {
		t.Errorf("property fill = %v; want red", got)
	}
}

func TestFitOverlaysBounds(t *testing.T) {
	o, err := OverlayFromWKT("POLYGON((-74.02 40.70, -74.00 40.70, -74.00 40.72, -74.02 40.72, -74.02 40.70))")
	if err != nil {
		t.Fatalf("OverlayFromWKT: %v", err)
	}

	lat, lng, zoom, err := FitOverlaysBounds([]Overlay{o}, 100, 100)
	if err != nil {
		t.Fatalf("FitOverlaysBounds: %v", err)
	}
	if lng < -74.02 || lng > -74.00 {
		t.Errorf("lng = %v; want within [-74.02, -74.00]", lng)
	}
	if lat < 40.70 || lat > 40.72 {
		t.Errorf("lat = %v; want within [40.70, 40.72]", lat)
	}
	if zoom < 0 || zoom > 22 {
		t.Errorf("zoom = %d; want within [0, 22]", zoom)
	}
}

func TestRenderOverlay(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte{})
	}))
	defer ts.Close()

	style := &MapStyle{
		Layers: []StyleLayer{
			{ID: "background", Type: "background", Paint: PaintProps{BackgroundColor: "#ffffff"}},
		},
	}

	overlay, err := OverlayFromWKT("POLYGON((-74.01 40.709, -74.002 40.709, -74.002 40.716, -74.01 40.716, -74.01 40.709))")
	if err != nil {
		t.Fatalf("OverlayFromWKT: %v", err)
	}
	overlay.FillColor = color.RGBA{255, 0, 0, 255}

	req := RenderRequest{
		CenterLat:        40.7128,
		CenterLng:        -74.0060,
		Zoom:             14,
		Width:            100,
		Height:           100,
		DevicePixelRatio: 1.0,
		Style:            style,
		TileURLTemplate:  ts.URL + "/{z}/{x}/{y}.pbf",
		Overlays:         []Overlay{overlay},
	}

	img, err := Render(context.Background(), req)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	red := 0
	for y := range 100 {
		for x := range 100 {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 > 200 && g>>8 < 80 && b>>8 < 80 {
				red++
			}
		}
	}
	if red == 0 {
		t.Errorf("expected red overlay pixels, got none")
	}
}
