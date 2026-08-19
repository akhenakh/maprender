package maprender

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers/rasterizer"
	"github.com/tdewolff/canvas/renderers/svg"
)

const (
	benchLat = 48.864716
	benchLng = 2.349014
)

var benchState = struct {
	once   sync.Once
	style  *MapStyle
	fonts  *FontManager
	server *httptest.Server
	err    error
}{}

func setupBench(b *testing.B) {
	benchState.once.Do(func() {
		style, err := FetchStyle("https://tiles.openfreemap.org/styles/liberty")
		if err != nil {
			benchState.err = err
			return
		}
		tj, err := style.ResolveTileJSON()
		if err != nil {
			benchState.err = err
			return
		}
		benchState.style = style
		benchState.fonts = DefaultFonts()
		benchState.server = newTileCacheServer(tj.Tiles[0])

		// warm up the tile cache for the benchmark zooms
		for _, z := range []int{14, 17} {
			_, _ = Render(context.Background(), benchReq(z))
		}
	})
	if benchState.err != nil {
		b.Fatalf("benchmark setup: %v", benchState.err)
	}
}

func benchReq(zoom int) RenderRequest {
	return RenderRequest{
		CenterLat:        benchLat,
		CenterLng:        benchLng,
		Zoom:             zoom,
		Width:            512,
		Height:           512,
		DevicePixelRatio: 1.0,
		Style:            benchState.style,
		Fonts:            benchState.fonts,
		TileURLTemplate:  benchState.server.URL + "/{z}/{x}/{y}.pbf",
		SourceMaxZoom:    14,
	}
}

func newTileCacheServer(template string) *httptest.Server {
	var mu sync.Mutex
	cache := map[string][]byte{}
	client := &http.Client{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		mu.Lock()
		data, ok := cache[key]
		mu.Unlock()
		if !ok {
			parts := strings.Split(strings.Trim(key, "/"), "/")
			if len(parts) < 3 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			url := strings.ReplaceAll(template, "{z}", parts[0])
			url = strings.ReplaceAll(url, "{x}", parts[1])
			url = strings.ReplaceAll(url, "{y}", strings.TrimSuffix(parts[2], ".pbf"))
			resp, err := client.Get(url)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			data, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
			mu.Lock()
			cache[key] = data
			mu.Unlock()
		}
		_, _ = w.Write(data)
	}))
}

func BenchmarkRender(b *testing.B) {
	setupBench(b)
	req := benchReq(17)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Render(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderCanvas(b *testing.B) {
	setupBench(b)
	req := benchReq(17)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := RenderCanvas(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderSVG(b *testing.B) {
	setupBench(b)
	req := benchReq(17)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c, err := RenderCanvas(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
		w := svg.New(io.Discard, c.W, c.H, nil)
		c.RenderTo(w)
		_ = w.Close()
	}
}

func BenchmarkRasterize(b *testing.B) {
	setupBench(b)
	c, err := RenderCanvas(context.Background(), benchReq(17))
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rasterizer.Draw(c, canvas.DPMM(1.0), canvas.LinearColorSpace{})
	}
}
