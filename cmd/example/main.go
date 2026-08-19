package main

import (
	"context"
	"flag"
	"image/png"
	"log"
	"os"

	"github.com/akhenakh/maprender"
	"github.com/tdewolff/canvas/renderers/svg"
)

func main() {
	svgOut := flag.Bool("svg", false, "output SVG instead of PNG")
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
