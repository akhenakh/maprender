package main

import (
	"context"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"time"

	"github.com/akhenakh/maprender"
	"github.com/tdewolff/canvas/renderers/svg"
)

func main() {
	svgOut := flag.Bool("svg", false, "output SVG instead of PNG")
	panOut := flag.Bool("pan", false, "demonstrate incremental panning: render a base frame, then pan east reusing pixels")
	flag.Parse()

	style, err := maprender.FetchStyle("https://tiles.openfreemap.org/styles/liberty")
	if err != nil {
		log.Fatalf("failed to fetch style: %v", err)
	}

	overlays, err := maprender.OverlayFromGeoJSON([]byte(`{
		"type": "Feature",
		"properties": {"fill": "#ff000080", "stroke": "#ff0000"},
		"geometry": {
			"type": "Polygon",
			"coordinates": [[
				[2.3482, 48.8638],
				[2.3498, 48.8638],
				[2.3498, 48.8654],
				[2.3482, 48.8654],
				[2.3482, 48.8638]
			]]
		}
	}`))
	if err != nil {
		log.Fatalf("failed to parse overlay: %v", err)
	}

	req := maprender.RenderRequest{
		CenterLat:        48.864716,
		CenterLng:        2.349014,
		Zoom:             17,
		Width:            512,
		Height:           512,
		DevicePixelRatio: 1.0,
		Style:            style,
		Overlays:         overlays,
	}

	if *panOut {
		start := time.Now()
		// A nil prev performs a full redraw and yields the first PanFrame.
		prev, err := maprender.RenderIncremental(context.Background(), req, nil)
		if err != nil {
			log.Fatalf("failed to render: %v", err)
		}
		baseTime := time.Since(start)

		// Pan east by a quarter of the viewport width.
		const shiftPx = 128
		panned := req
		panned.CenterLng += shiftPx / 512 * 360

		start = time.Now()
		img, err := maprender.RenderIncremental(context.Background(), panned, prev)
		if err != nil {
			log.Fatalf("failed to render pan: %v", err)
		}
		panTime := time.Since(start)

		f, err := os.Create("output_panned.png")
		if err != nil {
			log.Fatalf("failed to create output file: %v", err)
		}
		defer f.Close()
		if err := png.Encode(f, img.Image); err != nil {
			log.Fatalf("failed to encode PNG: %v", err)
		}
		fmt.Fprintf(os.Stderr, "full render: %v, incremental pan: %v (saved output_panned.png)\n", baseTime, panTime)
		return
	}

	if *svgOut {
		c, err := maprender.RenderCanvas(context.Background(), req)
		if err != nil {
			log.Fatalf("failed to render: %v", err)
		}

		f, err := os.Create("output.svg")
		if err != nil {
			log.Fatalf("failed to create output file: %v", err)
		}
		defer f.Close()

		w := svg.New(f, c.W, c.H, nil)
		c.RenderTo(w)
		if err := w.Close(); err != nil {
			log.Fatalf("failed to write SVG: %v", err)
		}

		log.Println("saved output.svg")
		return
	}

	img, err := maprender.Render(context.Background(), req)
	if err != nil {
		log.Fatalf("failed to render: %v", err)
	}

	f, err := os.Create("output.png")
	if err != nil {
		log.Fatalf("failed to create output file: %v", err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		log.Fatalf("failed to encode PNG: %v", err)
	}

	log.Println("saved output.png")
}
