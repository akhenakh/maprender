package maprender

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"image"
	"image/color"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/akhenakh/mvtgo"
	"github.com/fogleman/gg"
	"github.com/peterstace/simplefeatures/carto"
	"github.com/peterstace/simplefeatures/geom"
)

const TileSize = 512

type RenderRequest struct {
	CenterLat        float64
	CenterLng        float64
	Zoom             int
	Width            int // physical pixels
	Height           int // physical pixels
	DevicePixelRatio float64
	Style            *MapStyle
	TileURLTemplate  string
	SourceMinZoom    int // optional; when TileURLTemplate is set, use this source min zoom for underzoom
	SourceMaxZoom    int // optional; when TileURLTemplate is set, use this source max zoom for overzoom
	MarkerLat        *float64
	MarkerLng        *float64

	Logger *slog.Logger
}

func Render(ctx context.Context, req RenderRequest) (*image.RGBA, error) {
	logger := req.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	if req.Width == 0 || req.Height == 0 {
		return nil, fmt.Errorf("width or height is 0")
	}

	sourceMinZoom := 0
	sourceMaxZoom := 0
	if req.TileURLTemplate == "" {
		tj, err := req.Style.ResolveTileJSON()
		if err != nil {
			return nil, fmt.Errorf("TileURLTemplate is empty and could not be resolved from style: %w", err)
		}
		req.TileURLTemplate = tj.Tiles[0]
		sourceMinZoom = tj.MinZoom
		sourceMaxZoom = tj.MaxZoom
	} else {
		sourceMinZoom = req.SourceMinZoom
		sourceMaxZoom = req.SourceMaxZoom
	}

	logWidth := float64(req.Width) / req.DevicePixelRatio
	logHeight := float64(req.Height) / req.DevicePixelRatio

	logger.Debug("starting map render",
		"logWidth", logWidth, "logHeight", logHeight,
		"physicalWidth", req.Width, "physicalHeight", req.Height,
		"zoom", req.Zoom, "lat", req.CenterLat, "lng", req.CenterLng)

	wm := carto.NewWebMercator(req.Zoom)

	centerXY := wm.Forward(geom.XY{X: req.CenterLng, Y: req.CenterLat})
	globalPxX := centerXY.X * float64(TileSize)
	globalPxY := centerXY.Y * float64(TileSize)
	logger.Debug("computed center coordinates", "xyX", centerXY.X, "xyY", centerXY.Y, "globalPxX", globalPxX, "globalPxY", globalPxY)

	// Overzoom/underzoom: the source only serves tiles within
	// [sourceMinZoom, sourceMaxZoom]. When the client requests a zoom outside
	// that range, fetch the closest available zoom and scale the geometry to the
	// requested zoom instead of requesting (and failing on) non-existent tiles.
	srcZoom := req.Zoom
	if sourceMaxZoom > 0 && req.Zoom > sourceMaxZoom {
		srcZoom = sourceMaxZoom
	}
	if sourceMinZoom > 0 && req.Zoom < sourceMinZoom {
		srcZoom = sourceMinZoom
	}
	zoomScale := math.Exp2(float64(req.Zoom - srcZoom))
	tilePx := float64(TileSize) * zoomScale
	logger.Debug("source zoom resolution", "requestedZoom", req.Zoom, "srcZoom", srcZoom, "zoomScale", zoomScale)

	minPxX := globalPxX - logWidth/2
	minPxY := globalPxY - logHeight/2
	maxPxX := globalPxX + logWidth/2
	maxPxY := globalPxY + logHeight/2

	minTileX := int(math.Floor(minPxX / tilePx))
	minTileY := int(math.Floor(minPxY / tilePx))
	maxTileX := int(math.Floor(maxPxX / tilePx))
	maxTileY := int(math.Floor(maxPxY / tilePx))

	logger.Debug("pixel bounds", "minPxX", minPxX, "maxPxX", maxPxX, "minPxY", minPxY, "maxPxY", maxPxY)

	dc := gg.NewContext(req.Width, req.Height)
	dc.Scale(req.DevicePixelRatio, req.DevicePixelRatio)

	// Draw Background
	if bgLayer := GetLayerByID(req.Style, "background"); bgLayer != nil {
		dc.SetColor(parseColor(resolvePaintValue(bgLayer.Paint.BackgroundColor, float64(req.Zoom))))
	} else {
		dc.SetColor(color.RGBA{248, 244, 240, 255})
	}
	dc.Clear()

	logger.Debug("tiles required", "minTileX", minTileX, "maxTileX", maxTileX, "minTileY", minTileY, "maxTileY", maxTileY)

	for ty := minTileY; ty <= maxTileY; ty++ {
		for tx := minTileX; tx <= maxTileX; tx++ {
			if ctx.Err() != nil {
				logger.Debug("render cancelled by context")
				return nil, ctx.Err()
			}

			maxTiles := 1 << srcZoom
			wrapTx := (tx%maxTiles + maxTiles) % maxTiles

			url := strings.ReplaceAll(req.TileURLTemplate, "{z}", strconv.Itoa(srcZoom))
			url = strings.ReplaceAll(url, "{x}", strconv.Itoa(wrapTx))
			url = strings.ReplaceAll(url, "{y}", strconv.Itoa(ty))
			logger.Debug("fetching tile", "url", url)

			tileData, err := fetchTile(url)
			if err != nil {
				logger.Debug("failed to fetch tile", "url", url, "error", err)
				continue
			}

			logger.Debug("fetched tile", "bytes", len(tileData), "zoom", srcZoom, "x", wrapTx, "y", ty)

			collections, err := mvtgo.Decode(tileData)
			if err != nil {
				logger.Debug("failed to decode MVT tile data", "error", err)
				continue
			}

			logger.Debug("decoded tile layers", "count", len(collections))

			offsetX := float64(tx)*tilePx - minPxX
			offsetY := float64(ty)*tilePx - minPxY

			for _, layerStyle := range req.Style.Layers {
				if layerStyle.Type == "background" {
					continue
				}
				if layerStyle.MinZoom != nil && float64(req.Zoom) < *layerStyle.MinZoom {
					continue
				}
				if layerStyle.MaxZoom != nil && float64(req.Zoom) > *layerStyle.MaxZoom {
					continue
				}

				var mvtLayer *mvtgo.Layer
				for i, l := range collections {
					if l.Name == layerStyle.SourceLayer {
						mvtLayer = &collections[i]
						break
					}
				}
				if mvtLayer == nil {
					continue
				}

				scale := tilePx / float64(mvtLayer.Extent)
				renderedCount := 0
				filteredCount := 0

				for _, feature := range mvtLayer.Features {
					if !evaluateFilter(layerStyle.Filter, feature.Properties, feature.Geometry) {
						filteredCount++
						continue
					}

					drawFeature(dc, feature.Geometry, offsetX, offsetY, scale, req.DevicePixelRatio, &layerStyle, float64(req.Zoom))
					renderedCount++
				}

				if renderedCount > 0 {
					logger.Debug("rendered layer", "layer", layerStyle.ID, "source", layerStyle.SourceLayer, "rendered", renderedCount, "filtered", filteredCount)
				}
			}
		}
	}

	// Optional: Draw Marker
	if req.MarkerLat != nil && req.MarkerLng != nil {
		markerXY := wm.Forward(geom.XY{X: *req.MarkerLng, Y: *req.MarkerLat})
		mx := markerXY.X*float64(TileSize) - minPxX
		my := markerXY.Y*float64(TileSize) - minPxY

		logger.Debug("drawing marker at screen px", "x", mx, "y", my)

		dc.SetColor(color.RGBA{255, 0, 0, 255})
		dc.DrawCircle(mx, my, 6)
		dc.Fill()
		dc.SetColor(color.RGBA{255, 255, 255, 255})
		dc.DrawCircle(mx, my, 6)
		dc.SetLineWidth(2 * req.DevicePixelRatio)
		dc.Stroke()
	}

	return dc.Image().(*image.RGBA), nil
}

func drawFeature(dc *gg.Context, geometry geom.Geometry, offsetX, offsetY, scale, dpr float64, style *StyleLayer, zoom float64) {
	if geometry.IsEmpty() {
		return
	}
	if geometry.IsPolygon() {
		poly := geometry.MustAsPolygon()
		drawPolygon(dc, poly, offsetX, offsetY, scale, style, zoom)
	} else if geometry.IsMultiPolygon() {
		mp := geometry.MustAsMultiPolygon()
		for i := 0; i < mp.NumPolygons(); i++ {
			drawPolygon(dc, mp.PolygonN(i), offsetX, offsetY, scale, style, zoom)
		}
	} else if geometry.IsLineString() {
		ls := geometry.MustAsLineString()
		drawLineString(dc, ls, offsetX, offsetY, scale, dpr, style, zoom)
	} else if geometry.IsMultiLineString() {
		mls := geometry.MustAsMultiLineString()
		for i := 0; i < mls.NumLineStrings(); i++ {
			drawLineString(dc, mls.LineStringN(i), offsetX, offsetY, scale, dpr, style, zoom)
		}
	} else if geometry.IsPoint() {
		pt := geometry.MustAsPoint()
		drawPoint(dc, pt, offsetX, offsetY, scale, style, zoom)
	} else if geometry.IsMultiPoint() {
		mp := geometry.MustAsMultiPoint()
		for i := 0; i < mp.NumPoints(); i++ {
			drawPoint(dc, mp.PointN(i), offsetX, offsetY, scale, style, zoom)
		}
	}
}

func drawPolygon(dc *gg.Context, poly geom.Polygon, offsetX, offsetY, scale float64, style *StyleLayer, zoom float64) {
	c := resolvePaintValue(style.Paint.FillColor, zoom)
	if c == nil {
		return
	}
	dc.SetColor(parseColor(c))

	rings := poly.DumpRings()
	for _, ring := range rings {
		seq := ring.Coordinates()
		for i := 0; i < seq.Length(); i++ {
			xy := seq.GetXY(i)
			x := offsetX + xy.X*scale
			y := offsetY + xy.Y*scale
			if i == 0 {
				dc.MoveTo(x, y)
			} else {
				dc.LineTo(x, y)
			}
		}
		dc.ClosePath()
	}
	dc.Fill()
}

func drawLineString(dc *gg.Context, ls geom.LineString, offsetX, offsetY, scale, dpr float64, style *StyleLayer, zoom float64) {
	c := resolvePaintValue(style.Paint.LineColor, zoom)
	if c == nil {
		return
	}
	dc.SetColor(parseColor(c))
	lw := resolveLineWidth(style.Paint.LineWidth, zoom)
	dc.SetLineWidth(lw * dpr)

	seq := ls.Coordinates()
	for i := 0; i < seq.Length(); i++ {
		xy := seq.GetXY(i)
		x := offsetX + xy.X*scale
		y := offsetY + xy.Y*scale
		if i == 0 {
			dc.MoveTo(x, y)
		} else {
			dc.LineTo(x, y)
		}
	}
	dc.Stroke()
}

func drawPoint(dc *gg.Context, pt geom.Point, offsetX, offsetY, scale float64, style *StyleLayer, zoom float64) {
	c := resolvePaintValue(style.Paint.FillColor, zoom)
	if c == nil {
		return
	}
	dc.SetColor(parseColor(c))

	xy, ok := pt.XY()
	if !ok {
		return
	}
	x := offsetX + xy.X*scale
	y := offsetY + xy.Y*scale
	dc.DrawCircle(x, y, 3)
	dc.Fill()
}

func fetchTile(url string) ([]byte, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("User-Agent", "Charm-Bubbletea-MapViewer")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("bad status: %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if len(bodyBytes) >= 2 && bodyBytes[0] == 0x1F && bodyBytes[1] == 0x8B {
		gz, err := gzip.NewReader(bytes.NewReader(bodyBytes))
		if err == nil {
			uncompressed, err := io.ReadAll(gz)
			gz.Close()
			if err == nil {
				return uncompressed, nil
			}
		}
	}

	return bodyBytes, nil
}
