package maprender

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchSprite(t *testing.T) {
	sheet := image.NewRGBA(image.Rect(0, 0, 4, 1))
	cols := []color.RGBA{
		{255, 0, 0, 255},
		{0, 255, 0, 255},
		{0, 0, 255, 255},
		{255, 255, 255, 255},
	}
	for i, c := range cols {
		sheet.Set(i, 0, c)
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, sheet); err != nil {
		t.Fatalf("png encode: %v", err)
	}

	meta := map[string]spriteIconMeta{
		"red":   {X: 0, Y: 0, Width: 1, Height: 1, PixelRatio: 1},
		"green": {X: 1, Y: 0, Width: 1, Height: 1, PixelRatio: 1},
		"blue":  {X: 2, Y: 0, Width: 1, Height: 1, PixelRatio: 1},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/sprite.json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(meta)
	})
	mux.HandleFunc("/sprite.png", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(pngBuf.Bytes())
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	s, err := FetchSprite(ts.URL + "/sprite")
	if err != nil {
		t.Fatalf("FetchSprite: %v", err)
	}

	img, pr, ok := s.Icon("blue")
	if !ok {
		t.Fatal("blue icon not found")
	}
	if pr != 1 {
		t.Errorf("pixel ratio = %v; want 1", pr)
	}
	if b := img.Bounds(); b.Dx() != 1 || b.Dy() != 1 {
		t.Errorf("icon bounds = %v; want 1x1", b)
	}
	r, g, bb, a := img.At(0, 0).RGBA()
	if r>>8 != 0 || g>>8 != 0 || bb>>8 != 255 || a>>8 != 255 {
		t.Errorf("blue icon color = (%d,%d,%d,%d); want (0,0,255,255)", r>>8, g>>8, bb>>8, a>>8)
	}

	if _, _, ok := s.Icon("missing"); ok {
		t.Error("expected missing icon to return ok=false")
	}
}
