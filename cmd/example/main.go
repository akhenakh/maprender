package main

import (
	"context"
	"image/png"
	"log"
	"os"

	"github.com/akhenakh/maprender"
)

func main() {
	style, err := maprender.FetchStyle("https://tiles.openfreemap.org/styles/liberty")
	if err != nil {
		log.Fatalf("failed to fetch style: %v", err)
	}

	req := maprender.RenderRequest{
		CenterLat:        48.864716,
		CenterLng:        2.349014,
		Zoom:             17,
		Width:            512,
		Height:           512,
		DevicePixelRatio: 1.0,
		Style:            style,
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
