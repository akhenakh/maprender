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
	"github.com/peterstace/simplefeatures/carto"
	"github.com/peterstace/simplefeatures/geom"
	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers/rasterizer"
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
	Fonts            *FontManager
	Sprite           *Sprite
	Overlays         []Overlay
	FitOverlays      bool // when true, center and zoom are computed to fit Overlays
	MarkerLat        *float64
	MarkerLng        *float64

	Logger *slog.Logger
}

// Render renders the map to a raster image (PNG-ready *image.RGBA).
func Render(ctx context.Context, req RenderRequest) (*image.RGBA, error) {
	c, err := RenderCanvas(ctx, req)
	if err != nil {
		return nil, err
	}

	dpr := req.DevicePixelRatio
	if dpr <= 0 {
		dpr = 1
	}
	return rasterizer.Draw(c, canvas.DPMM(dpr), canvas.LinearColorSpace{}), nil
}

// RenderCanvas renders the map to a vector canvas that can be rasterized or
// exported to other formats (SVG, PDF, EPS, ...) via canvas.Write / WriteFile.
func RenderCanvas(ctx context.Context, req RenderRequest) (*canvas.Canvas, error) {
	logger := req.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	if req.Width == 0 || req.Height == 0 {
		return nil, fmt.Errorf("width or height is 0")
	}

	dpr := req.DevicePixelRatio
	if dpr <= 0 {
		dpr = 1
	}

	fonts := req.Fonts
	if fonts == nil {
		fonts = DefaultFonts()
	}

	sprite := req.Sprite
	if sprite == nil && req.Style.SpriteURL != "" {
		if s, err := fetchSpriteCached(req.Style.SpriteURL); err == nil {
			sprite = s
		} else {
			logger.Debug("failed to fetch sprite", "error", err)
		}
	}

	// Map geometry is simple polylines/polygons with many shared edges; the
	// canvas library's exact stroke "settling" (boolean ops) is prohibitively
	// expensive and can even hit degenerate cases on this kind of data.
	canvas.FastStroke = true
	canvas.Tolerance = 0.1

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

	logWidth := float64(req.Width) / dpr
	logHeight := float64(req.Height) / dpr

	// Fit the view to the overlays' bounds when requested.
	if req.FitOverlays && len(req.Overlays) > 0 {
		lat, lng, zoom, err := FitOverlaysBounds(req.Overlays, logWidth, logHeight)
		if err != nil {
			logger.Debug("failed to fit overlays", "error", err)
		} else {
			req.CenterLat, req.CenterLng, req.Zoom = lat, lng, zoom
			logger.Debug("fit overlays", "lat", lat, "lng", lng, "zoom", zoom)
		}
	}

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

	c := canvas.New(logWidth, logHeight)
	dc := canvas.NewContext(c)
	dc.SetCoordSystem(canvas.CartesianIV)

	// Draw Background
	if bgLayer := GetLayerByID(req.Style, "background"); bgLayer != nil {
		dc.SetFillColor(parseColor(resolvePaintValue(bgLayer.Paint.BackgroundColor, float64(req.Zoom))))
	} else {
		dc.SetFillColor(color.RGBA{248, 244, 240, 255})
	}
	dc.MoveTo(0, 0)
	dc.LineTo(logWidth, 0)
	dc.LineTo(logWidth, logHeight)
	dc.LineTo(0, logHeight)
	dc.Close()
	dc.Fill()

	logger.Debug("tiles required", "minTileX", minTileX, "maxTileX", maxTileX, "minTileY", minTileY, "maxTileY", maxTileY)

	type decodedTile struct {
		offsetX     float64
		offsetY     float64
		collections []mvtgo.Layer
	}

	var tiles []decodedTile
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

			tiles = append(tiles, decodedTile{
				offsetX:     float64(tx)*tilePx - minPxX,
				offsetY:     float64(ty)*tilePx - minPxY,
				collections: collections,
			})
		}
	}

	// Pass 1: fill and line layers (background is already painted).
	for _, tile := range tiles {
		for _, layerStyle := range req.Style.Layers {
			if layerStyle.Type == "background" || layerStyle.Type == "symbol" {
				continue
			}
			if !layerVisible(layerStyle, req.Zoom) {
				continue
			}

			mvtLayer := findLayer(tile.collections, layerStyle.SourceLayer)
			if mvtLayer == nil {
				continue
			}

			scale := tilePx / float64(mvtLayer.Extent)
			for _, feature := range mvtLayer.Features {
				if !featureOnScreen(feature.Geometry, tile.offsetX, tile.offsetY, scale, logWidth, logHeight, 64) {
					continue
				}
				if !evaluateFilter(layerStyle.Filter, feature.Properties, feature.Geometry) {
					continue
				}
				drawFeature(dc, feature.Geometry, tile.offsetX, tile.offsetY, scale, &layerStyle, float64(req.Zoom))
			}
		}
	}

	// Pass 2: symbol (text) layers, drawn in reverse style order so that
	// higher-priority labels (place/city/country, declared last in the style)
	// reserve space first. Collision detection prevents overlapping labels.
	grid := newCollisionGrid(32)
	for _, tile := range tiles {
		for i := len(req.Style.Layers) - 1; i >= 0; i-- {
			layerStyle := &req.Style.Layers[i]
			if layerStyle.Type != "symbol" {
				continue
			}
			if !layerVisible(*layerStyle, req.Zoom) {
				continue
			}

			mvtLayer := findLayer(tile.collections, layerStyle.SourceLayer)
			if mvtLayer == nil {
				continue
			}

			scale := tilePx / float64(mvtLayer.Extent)
			for _, feature := range mvtLayer.Features {
				if !featureOnScreen(feature.Geometry, tile.offsetX, tile.offsetY, scale, logWidth, logHeight, 64) {
					continue
				}
				if !evaluateFilter(layerStyle.Filter, feature.Properties, feature.Geometry) {
					continue
				}
				drawSymbolFeature(dc, feature.Geometry, feature.Properties, tile.offsetX, tile.offsetY, scale, layerStyle, float64(req.Zoom), fonts, sprite, grid)
			}
		}
	}

	// Optional: Draw Marker
	if req.MarkerLat != nil && req.MarkerLng != nil {
		markerXY := wm.Forward(geom.XY{X: *req.MarkerLng, Y: *req.MarkerLat})
		mx := markerXY.X*float64(TileSize) - minPxX
		my := markerXY.Y*float64(TileSize) - minPxY

		logger.Debug("drawing marker at screen px", "x", mx, "y", my)

		dc.SetFillColor(color.RGBA{255, 0, 0, 255})
		dc.DrawPath(mx, my, canvas.Circle(6))
		dc.Fill()
		dc.SetStrokeColor(color.RGBA{255, 255, 255, 255})
		dc.SetStrokeWidth(2)
		dc.DrawPath(mx, my, canvas.Circle(6))
		dc.Stroke()
	}

	// Draw overlays on top of the map.
	for _, o := range req.Overlays {
		if o.Geometry.IsEmpty() {
			continue
		}
		strokeWidth := o.StrokeWidth
		if strokeWidth <= 0 {
			strokeWidth = 2
		}
		projected := projectGeometry(o.Geometry, wm, minPxX, minPxY)
		drawOverlayGeometry(dc, projected, o.strokeColor(), o.fillColor(), strokeWidth)
	}

	return c, nil
}

func drawFeature(dc *canvas.Context, geometry geom.Geometry, offsetX, offsetY, scale float64, style *StyleLayer, zoom float64) {
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
		drawLineString(dc, ls, offsetX, offsetY, scale, style, zoom)
	} else if geometry.IsMultiLineString() {
		mls := geometry.MustAsMultiLineString()
		for i := 0; i < mls.NumLineStrings(); i++ {
			drawLineString(dc, mls.LineStringN(i), offsetX, offsetY, scale, style, zoom)
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

func drawPolygon(dc *canvas.Context, poly geom.Polygon, offsetX, offsetY, scale float64, style *StyleLayer, zoom float64) {
	c := resolvePaintValue(style.Paint.FillColor, zoom)
	if c == nil {
		return
	}
	dc.SetFillColor(parseColor(c))

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
		dc.Close()
	}
	dc.Fill()
}

func drawLineString(dc *canvas.Context, ls geom.LineString, offsetX, offsetY, scale float64, style *StyleLayer, zoom float64) {
	c := resolvePaintValue(style.Paint.LineColor, zoom)
	if c == nil {
		return
	}
	dc.SetStrokeColor(parseColor(c))
	lw := resolveLineWidth(style.Paint.LineWidth, zoom)
	dc.SetStrokeWidth(lw)

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

func drawPoint(dc *canvas.Context, pt geom.Point, offsetX, offsetY, scale float64, style *StyleLayer, zoom float64) {
	c := resolvePaintValue(style.Paint.FillColor, zoom)
	if c == nil {
		return
	}
	dc.SetFillColor(parseColor(c))

	xy, ok := pt.XY()
	if !ok {
		return
	}
	x := offsetX + xy.X*scale
	y := offsetY + xy.Y*scale
	dc.DrawPath(x, y, canvas.Circle(3))
	dc.Fill()
}

func drawSymbolFeature(dc *canvas.Context, geometry geom.Geometry, props map[string]any, offsetX, offsetY, scale float64, style *StyleLayer, zoom float64, fonts *FontManager, sprite *Sprite, grid *collisionGrid) {
	if geometry.IsEmpty() {
		return
	}

	iconName := resolveIconImage(style.Layout.IconImage, props, geometry, zoom)
	text := resolveTextField(style.Layout.TextField, props, geometry)
	if text != "" {
		if tr := resolveTextTransform(style.Layout.TextTransform, zoom); tr != "" {
			text = applyTextTransform(text, tr)
		}
	}
	if iconName == "" && text == "" {
		return
	}

	anchor, angle, ok := symbolAnchor(geometry)
	if !ok {
		return
	}
	x := offsetX + anchor.X*scale
	y := offsetY + anchor.Y*scale

	// Keep labels readable: when the street direction points "down" (which
	// would render the text upside-down), flip the label by 180 degrees.
	if angle > 90 || angle < -90 {
		angle += 180
	}
	for angle > 180 {
		angle -= 360
	}
	for angle < -180 {
		angle += 360
	}

	// Resolve the icon (centered on the anchor) if the layer has one.
	var iconImg image.Image
	var iconW, iconH float64
	if iconName != "" && sprite != nil {
		if img, pr, ok := sprite.Icon(iconName); ok && img != nil {
			iconSize := resolveIconSize(style.Layout.IconSize, zoom)
			if pr <= 0 {
				pr = 1
			}
			iconW = float64(img.Bounds().Dx()) / pr * iconSize
			iconH = float64(img.Bounds().Dy()) / pr * iconSize
			iconImg = img
		}
	}

	// Resolve the text face.
	var face *canvas.FontFace
	var m canvas.FontMetrics
	var textW float64
	halign := canvas.Center
	var dy float64
	if text != "" {
		size := resolveTextSize(style.Layout.TextSize, zoom)

		col := color.Color(color.RGBA{0, 0, 0, 255})
		if c := resolvePaintValue(style.Paint.TextColor, zoom); c != nil {
			col = parseColor(c)
		}

		var haloColor color.Color
		haloWidth := 0.0
		if c := resolvePaintValue(style.Paint.TextHaloColor, zoom); c != nil {
			haloColor = parseColor(c)
		}
		if hw := resolvePaintValue(style.Paint.TextHaloWidth, zoom); hw != nil {
			if f, ok := toFloat(hw); ok {
				haloWidth = f
			}
		}

		face = fonts.Face(style.Layout.TextFont, size, col, haloColor, haloWidth)
		if face != nil {
			m = face.Metrics()
			textW = face.TextWidth(text)
			halign, dy = anchorLayout(style.Layout.TextAnchor, face)
		}
	}

	// Offset the text away from the icon so they don't overlap.
	textAnchor := "center"
	if s, ok := style.Layout.TextAnchor.(string); ok && s != "" {
		textAnchor = s
	}
	var tdx, tdy float64
	if iconImg != nil {
		tdx, tdy = iconTextOffset(textAnchor, iconW, iconH)
	}

	// Collision bounding box: union of the icon and text boxes (unrotated),
	// then the axis-aligned box of that rectangle under rotation.
	var box [4]float64 // minX, minY, maxX, maxY
	box[0], box[1] = math.Inf(1), math.Inf(1)
	box[2], box[3] = math.Inf(-1), math.Inf(-1)
	if iconImg != nil {
		box[0] = math.Min(box[0], x-iconW/2)
		box[1] = math.Min(box[1], y-iconH/2)
		box[2] = math.Max(box[2], x+iconW/2)
		box[3] = math.Max(box[3], y+iconH/2)
	}
	if text != "" && face != nil {
		tx := x + tdx
		baseline := y + dy + tdy
		var cx float64
		switch halign {
		case canvas.Left:
			cx = tx + textW/2
		case canvas.Right:
			cx = tx - textW/2
		default:
			cx = tx
		}
		cy := baseline + (m.Descent-m.Ascent)/2
		box[0] = math.Min(box[0], cx-textW/2)
		box[1] = math.Min(box[1], cy-(m.Ascent+m.Descent)/2)
		box[2] = math.Max(box[2], cx+textW/2)
		box[3] = math.Max(box[3], cy+(m.Ascent+m.Descent)/2)
	}

	bcx := (box[0] + box[2]) / 2
	bcy := (box[1] + box[3]) / 2
	hw := (box[2] - box[0]) / 2
	hh := (box[3] - box[1]) / 2
	th := angle * math.Pi / 180
	cw := math.Abs(math.Cos(th))*hw + math.Abs(math.Sin(th))*hh
	ch := math.Abs(math.Sin(th))*hw + math.Abs(math.Cos(th))*hh

	const pad = 2.0
	x0 := bcx - cw - pad
	x1 := bcx + cw + pad
	y0 := bcy - ch - pad
	y1 := bcy + ch + pad

	if grid != nil && grid.overlaps(x0, y0, x1, y1) {
		return
	}

	if angle != 0 {
		dc.Push()
		dc.RotateAbout(angle, x, y)
	}
	if iconImg != nil {
		resolution := canvas.DPMM(float64(iconImg.Bounds().Dx()) / iconW)
		dc.DrawImage(x-iconW/2, y-iconH/2, iconImg, resolution)
	}
	if text != "" && face != nil {
		dc.DrawText(x+tdx, y+dy+tdy, canvas.NewTextLine(face, text, halign))
	}
	if angle != 0 {
		dc.Pop()
	}

	if grid != nil {
		grid.add(x0, y0, x1, y1)
	}
}

func layerVisible(l StyleLayer, zoom int) bool {
	if l.MinZoom != nil && float64(zoom) < *l.MinZoom {
		return false
	}
	if l.MaxZoom != nil && float64(zoom) > *l.MaxZoom {
		return false
	}
	return true
}

// featureOnScreen reports whether a feature's bounding box (transformed to
// screen coordinates) intersects the viewport, expanded by pad pixels to
// account for stroke widths and label extents.
func featureOnScreen(g geom.Geometry, offsetX, offsetY, scale, viewW, viewH, pad float64) bool {
	env := g.Envelope()
	minXY, maxXY, ok := env.MinMaxXYs()
	if !ok {
		return true
	}
	x0 := offsetX + minXY.X*scale
	x1 := offsetX + maxXY.X*scale
	y0 := offsetY + minXY.Y*scale
	y1 := offsetY + maxXY.Y*scale
	return x1 >= -pad && x0 <= viewW+pad && y1 >= -pad && y0 <= viewH+pad
}

func findLayer(collections []mvtgo.Layer, name string) *mvtgo.Layer {
	for i := range collections {
		if collections[i].Name == name {
			return &collections[i]
		}
	}
	return nil
}

type collisionGrid struct {
	cellSize float64
	cells    map[[2]int][][4]float64
}

func newCollisionGrid(cellSize float64) *collisionGrid {
	return &collisionGrid{cellSize: cellSize, cells: map[[2]int][][4]float64{}}
}

func (g *collisionGrid) overlaps(x0, y0, x1, y1 float64) bool {
	cx0 := int(math.Floor(x0 / g.cellSize))
	cx1 := int(math.Floor(x1 / g.cellSize))
	cy0 := int(math.Floor(y0 / g.cellSize))
	cy1 := int(math.Floor(y1 / g.cellSize))
	for cy := cy0; cy <= cy1; cy++ {
		for cx := cx0; cx <= cx1; cx++ {
			for _, r := range g.cells[[2]int{cx, cy}] {
				if x0 < r[2] && x1 > r[0] && y0 < r[3] && y1 > r[1] {
					return true
				}
			}
		}
	}
	return false
}

func (g *collisionGrid) add(x0, y0, x1, y1 float64) {
	cx0 := int(math.Floor(x0 / g.cellSize))
	cx1 := int(math.Floor(x1 / g.cellSize))
	cy0 := int(math.Floor(y0 / g.cellSize))
	cy1 := int(math.Floor(y1 / g.cellSize))
	for cy := cy0; cy <= cy1; cy++ {
		for cx := cx0; cx <= cx1; cx++ {
			key := [2]int{cx, cy}
			g.cells[key] = append(g.cells[key], [4]float64{x0, y0, x1, y1})
		}
	}
}

// symbolAnchor returns a representative coordinate (in tile-local units) used
// to place a symbol's label, together with the tangent angle (in degrees) of
// the underlying line. For non-line geometries the angle is 0 (horizontal
// text).
func symbolAnchor(geometry geom.Geometry) (geom.XY, float64, bool) {
	if geometry.IsEmpty() {
		return geom.XY{}, 0, false
	}
	switch {
	case geometry.IsLineString():
		return lineAnchor(geometry.MustAsLineString())
	case geometry.IsMultiLineString():
		mls := geometry.MustAsMultiLineString()
		best := -1
		var bestLen float64
		for i := 0; i < mls.NumLineStrings(); i++ {
			l := mls.LineStringN(i).Length()
			if best == -1 || l > bestLen {
				bestLen = l
				best = i
			}
		}
		if best == -1 {
			return geom.XY{}, 0, false
		}
		return lineAnchor(mls.LineStringN(best))
	default:
		xy, ok := geometry.Centroid().XY()
		return xy, 0, ok
	}
}

// lineAnchor returns the coordinate at the midpoint of the line (by length)
// and the tangent angle (in degrees, screen orientation where y grows down) of
// the segment containing that midpoint.
func lineAnchor(ls geom.LineString) (geom.XY, float64, bool) {
	coords := ls.Coordinates()
	n := coords.Length()
	if n == 0 {
		return geom.XY{}, 0, false
	}
	if n == 1 {
		return coords.GetXY(0), 0, true
	}

	var total float64
	lens := make([]float64, n-1)
	for i := 0; i < n-1; i++ {
		a := coords.GetXY(i)
		b := coords.GetXY(i + 1)
		l := math.Hypot(b.X-a.X, b.Y-a.Y)
		lens[i] = l
		total += l
	}
	if total == 0 {
		return coords.GetXY(0), 0, true
	}

	half := total / 2
	for i, l := range lens {
		if half <= l {
			a := coords.GetXY(i)
			b := coords.GetXY(i + 1)
			t := half / l
			px := a.X + (b.X-a.X)*t
			py := a.Y + (b.Y-a.Y)*t
			angle := math.Atan2(b.Y-a.Y, b.X-a.X) * 180 / math.Pi
			return geom.XY{X: px, Y: py}, angle, true
		}
		half -= l
	}

	return coords.GetXY(n - 1), 0, true
}

func anchorLayout(anchorVal any, face *canvas.FontFace) (canvas.TextAlign, float64) {
	anchor := "center"
	if s, ok := anchorVal.(string); ok && s != "" {
		anchor = s
	}

	m := face.Metrics()
	halign := canvas.Center
	var dy float64

	switch anchor {
	case "left", "top-left", "bottom-left":
		halign = canvas.Left
	case "right", "top-right", "bottom-right":
		halign = canvas.Right
	}

	switch anchor {
	case "top", "top-left", "top-right":
		dy = m.Ascent
	case "bottom", "bottom-left", "bottom-right":
		dy = -m.Descent
	default:
		dy = (m.Ascent - m.Descent) / 2
	}

	return halign, dy
}

// iconTextOffset returns how much to shift the text anchor so it sits next to
// (instead of on top of) the icon, based on the text-anchor direction.
func iconTextOffset(anchor string, iconW, iconH float64) (dx, dy float64) {
	const gap = 2.0
	switch anchor {
	case "top", "top-left", "top-right":
		dy = iconH/2 + gap
	case "bottom", "bottom-left", "bottom-right":
		dy = -(iconH/2 + gap)
	}
	switch anchor {
	case "left", "top-left", "bottom-left":
		dx = iconW/2 + gap
	case "right", "top-right", "bottom-right":
		dx = -(iconW/2 + gap)
	}
	if anchor == "" || anchor == "center" {
		dy = iconH/2 + gap
	}
	return
}

func resolveTextTransform(val any, zoom float64) string {
	if val == nil {
		return ""
	}
	if s, ok := val.(string); ok {
		return s
	}
	if r := resolvePaintValue(val, zoom); r != nil {
		if s, ok := r.(string); ok {
			return s
		}
	}
	return ""
}

func applyTextTransform(text, transform string) string {
	switch transform {
	case "uppercase":
		return strings.ToUpper(text)
	case "lowercase":
		return strings.ToLower(text)
	}
	return text
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
