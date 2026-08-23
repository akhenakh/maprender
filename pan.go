package maprender

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"math"

	"github.com/peterstace/simplefeatures/carto"
	"github.com/peterstace/simplefeatures/geom"
	"github.com/tdewolff/canvas"
	"github.com/tdewolff/canvas/renderers/rasterizer"
)

// panPadPx is the extra margin (in logical pixels) rendered around each newly
// exposed region so that strokes crossing the seam blend seamlessly with the
// pixels reused from the previous frame. Patches carry no text labels (those
// are drawn in a single full-viewport pass afterwards), so this only needs to
// cover stroke widths and overlay lines.
const panPadPx = 16.0

// PanFrame is the result of an incremental pan. Image is the complete frame
// (text labels, icons and marker included) ready for display; Base is the same
// frame without labels — pass it back as Prev on the next RenderIncremental
// call so labels are never stacked onto already drawn ones.
type PanFrame struct {
	Image *image.RGBA
	Base  *image.RGBA

	CenterLat float64
	CenterLng float64
	Zoom      int
}

// RenderIncremental renders req while reusing pixels from prev (the PanFrame
// returned by an earlier call at the same zoom, size and style): the previous
// label-free base is shifted according to the pan delta, only the newly
// exposed strips are re-rendered, and text labels/icons/marker are drawn
// fresh onto a copy of the result. This makes panning dramatically cheaper
// than a full render while keeping every frame identical to one.
//
// Everything else in req must be unchanged since prev was produced; when that
// does not hold (zoom change, resize, new overlays, ...) use Render instead.
// A nil prev (or a mismatching one) performs a full redraw through the same
// pipeline, so callers do not need special cases.
func RenderIncremental(ctx context.Context, req RenderRequest, prev *PanFrame) (*PanFrame, error) {
	if req.Width == 0 || req.Height == 0 {
		return nil, fmt.Errorf("width or height is 0")
	}

	dpr := req.DevicePixelRatio
	if dpr <= 0 {
		dpr = 1
	}

	wm := carto.NewWebMercator(req.Zoom)
	newC := wm.Forward(geom.XY{X: req.CenterLng, Y: req.CenterLat})

	var prevBase *image.RGBA
	if prev != nil && prev.Base != nil && prev.Zoom == req.Zoom &&
		prev.Base.Bounds().Dx() == req.Width && prev.Base.Bounds().Dy() == req.Height {
		prevBase = prev.Base
	}

	// Shift of the previous base content in physical pixels.
	var sx, sy int
	if prevBase != nil {
		oldC := wm.Forward(geom.XY{X: prev.CenterLng, Y: prev.CenterLat})
		sx = int(math.Round((oldC.X - newC.X) * TileSize * dpr))
		sy = int(math.Round((oldC.Y - newC.Y) * TileSize * dpr))
	}

	dst := image.NewRGBA(image.Rect(0, 0, req.Width, req.Height))

	var bands []image.Rectangle
	switch {
	case prevBase == nil || absInt(sx) >= req.Width || absInt(sy) >= req.Height:
		// Nothing reusable: redraw everything through the patch pipeline.
		bands = append(bands, image.Rect(0, 0, req.Width, req.Height))
	default:
		draw.Draw(dst, image.Rect(sx, sy, sx+req.Width, sy+req.Height), prevBase, image.Point{}, draw.Src)
		if sx > 0 {
			bands = append(bands, image.Rect(0, 0, sx, req.Height))
		} else if sx < 0 {
			bands = append(bands, image.Rect(req.Width+sx, 0, req.Width, req.Height))
		}
		if sy > 0 {
			bands = append(bands, image.Rect(0, 0, req.Width, sy))
		} else if sy < 0 {
			bands = append(bands, image.Rect(0, req.Height+sy, req.Width, req.Height))
		}
	}

	// Fetch and decode the tiles of the new viewport once; patches and the
	// label overlay below reuse them (decoded tiles are also memoized across
	// pans, see fetchTiles).
	vp, err := prepareView(ctx, req)
	if err != nil {
		return nil, err
	}
	tiles, err := fetchTiles(ctx, vp)
	if err != nil {
		return nil, err
	}

	logW := float64(req.Width) / dpr
	logH := float64(req.Height) / dpr
	globalCx := newC.X * float64(TileSize)
	globalCy := newC.Y * float64(TileSize)

	for _, r := range bands {
		r = r.Intersect(dst.Bounds())
		if r.Empty() {
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		renderPatch(vp, tiles, dst, r, wm, globalCx, globalCy, logW, logH, dpr)
	}

	// The base keeps the geometry-only pixels for the next pan; labels go on
	// a fresh copy so they never stack onto previously drawn ones.
	base := dst
	frame := image.NewRGBA(dst.Bounds())
	draw.Draw(frame, frame.Bounds(), dst, image.Point{}, draw.Src)

	// Label placement relies on viewport-wide collision detection, so labels
	// cannot be rendered per-patch without seam artifacts; drawing them once
	// over the whole viewport keeps every frame identical to a full render.
	// Only the drawn label boxes are blended, which keeps the composite cost
	// proportional to the label count instead of the frame size.
	overlay, boxes := drawSymbolOverlayCanvas(vp, tiles)
	for _, r := range boxes {
		draw.Draw(frame, r, overlay, r.Min, draw.Over)
	}

	return &PanFrame{
		Image:     frame,
		Base:      base,
		CenterLat: req.CenterLat,
		CenterLng: req.CenterLng,
		Zoom:      req.Zoom,
	}, nil
}

// renderPatch renders the newly exposed region r of dst (physical pixel
// coordinates in the new viewport), expanded by a padding margin so geometry
// crossing the seam is drawn without clipping artifacts, and pastes it onto
// dst at the exact exposure position. The already decoded tiles of the full
// viewport are repositioned for the patch viewport instead of being fetched
// and decoded again.
func renderPatch(vp *viewParams, tiles []decodedTile, dst *image.RGBA, r image.Rectangle, wm *carto.WebMercator, globalCx, globalCy, logW, logH, dpr float64) {
	margin := int(math.Ceil(panPadPx * dpr))
	patch := image.Rect(r.Min.X-margin, r.Min.Y-margin, r.Max.X+margin, r.Max.Y+margin).
		Intersect(dst.Bounds())
	if patch.Empty() {
		return
	}

	patchW := float64(patch.Dx()) / dpr
	patchH := float64(patch.Dy()) / dpr

	// World coordinates of the patch center.
	gx := globalCx - logW/2 + float64(patch.Min.X)/dpr + patchW/2
	gy := globalCy - logH/2 + float64(patch.Min.Y)/dpr + patchH/2
	world := wm.Reverse(geom.XY{X: gx / TileSize, Y: gy / TileSize})

	sub := subViewParams(vp, world.Y, world.X, patch.Dx(), patch.Dy())

	// Reposition the already decoded tiles for the patch viewport and drop
	// those not overlapping it. MVT geometries are clipped to their tile, so
	// a small margin suffices.
	pad := panPadPx
	subTiles := make([]decodedTile, 0, len(tiles))
	for _, t := range tiles {
		ox := t.offsetX + vp.minPxX - sub.minPxX
		oy := t.offsetY + vp.minPxY - sub.minPxY
		// Keep tiles whose span [ox, ox+tilePx] overlaps the patch viewport.
		if ox+vp.tilePx < -pad || oy+vp.tilePx < -pad ||
			ox > sub.logW+pad || oy > sub.logH+pad {
			continue
		}
		subTiles = append(subTiles, decodedTile{offsetX: ox, offsetY: oy, collections: t.collections, envs: t.envs})
	}

	c := canvas.New(sub.logW, sub.logH)
	dc := canvas.NewContext(c)
	dc.SetCoordSystem(canvas.CartesianIV)

	drawBackground(dc, sub)
	drawGeometryLayers(dc, sub, subTiles)
	drawOverlays(dc, sub)

	img := rasterizer.Draw(c, canvas.DPMM(dpr), canvas.LinearColorSpace{})
	draw.Draw(dst, patch, img, img.Bounds().Min, draw.Src)
}

// drawSymbolOverlayCanvas renders the symbol layers and the marker for vp's
// viewport onto a transparent canvas. It returns the rasterized overlay and
// the bounding boxes (physical pixels) of everything drawn, so callers can
// composite just those regions.
func drawSymbolOverlayCanvas(vp *viewParams, tiles []decodedTile) (*image.RGBA, []image.Rectangle) {
	c := canvas.New(vp.logW, vp.logH)
	dc := canvas.NewContext(c)
	dc.SetCoordSystem(canvas.CartesianIV)

	var logicalBoxes [][4]float64
	drawSymbolLayers(dc, vp, tiles, &logicalBoxes)
	if box, ok := drawMarker(dc, vp); ok {
		logicalBoxes = append(logicalBoxes, box)
	}

	dpr := vp.req.DevicePixelRatio
	if dpr <= 0 {
		dpr = 1
	}
	img := rasterizer.Draw(c, canvas.DPMM(dpr), canvas.LinearColorSpace{})

	bounds := img.Bounds()
	rects := make([]image.Rectangle, 0, len(logicalBoxes))
	const blendPad = 2 // antialiasing and halo spread beyond the collision box
	for _, b := range logicalBoxes {
		r := image.Rect(
			int(math.Floor(b[0]*dpr))-blendPad,
			int(math.Floor(b[1]*dpr))-blendPad,
			int(math.Ceil(b[2]*dpr))+blendPad,
			int(math.Ceil(b[3]*dpr))+blendPad,
		).Intersect(bounds)
		if !r.Empty() {
			rects = append(rects, r)
		}
	}
	return img, rects
}

// subViewParams returns a copy of vp whose viewport is the given physical
// size, centered on the given coordinates.
func subViewParams(vp *viewParams, centerLat, centerLng float64, width, height int) *viewParams {
	sub := *vp
	sub.req = vp.req
	sub.req.CenterLat = centerLat
	sub.req.CenterLng = centerLng
	sub.req.FitOverlays = false
	sub.req.Width = width
	sub.req.Height = height

	dpr := vp.req.DevicePixelRatio
	if dpr <= 0 {
		dpr = 1
	}
	sub.logW = float64(width) / dpr
	sub.logH = float64(height) / dpr

	cxy := vp.wm.Forward(geom.XY{X: centerLng, Y: centerLat})
	sub.minPxX = cxy.X*float64(TileSize) - sub.logW/2
	sub.minPxY = cxy.Y*float64(TileSize) - sub.logH/2

	sub.minTileX = int(math.Floor(sub.minPxX / vp.tilePx))
	sub.minTileY = int(math.Floor(sub.minPxY / vp.tilePx))
	sub.maxTileX = int(math.Floor((sub.minPxX + sub.logW) / vp.tilePx))
	sub.maxTileY = int(math.Floor((sub.minPxY + sub.logH) / vp.tilePx))
	return &sub
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
