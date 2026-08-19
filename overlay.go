package maprender

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"math"

	"github.com/peterstace/simplefeatures/carto"
	"github.com/peterstace/simplefeatures/geom"
	"github.com/tdewolff/canvas"
)

// Overlay is a geometry (in WGS84 / lon-lat coordinates) drawn on top of the
// rendered map.
type Overlay struct {
	// Geometry is the geometry to draw. Coordinates are interpreted as
	// (longitude, latitude).
	Geometry geom.Geometry

	// Properties are optional free-form properties (e.g. from a GeoJSON
	// feature). They are used to derive stroke/fill colors when the explicit
	// colors below are nil.
	Properties map[string]any

	// StrokeColor is the outline color. When nil, it is derived from
	// Properties (keys "stroke", "stroke-color", "strokeColor") and finally
	// defaults to red.
	StrokeColor color.Color

	// FillColor is the polygon fill color. When nil, it is derived from
	// Properties (keys "fill", "fill-color", "fillColor") and finally defaults
	// to transparent (no fill).
	FillColor color.Color

	// StrokeWidth is the outline width in pixels. Zero means the default (2).
	StrokeWidth float64
}

// OverlayFromGeoJSON parses GeoJSON (a Geometry, Feature, or
// FeatureCollection) into overlays. Feature properties are retained for color
// extraction.
func OverlayFromGeoJSON(data []byte) ([]Overlay, error) {
	data = bytes.TrimSpace(data)

	var typ struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &typ); err != nil {
		return nil, err
	}

	switch typ.Type {
	case "FeatureCollection":
		var fc geom.GeoJSONFeatureCollection
		if err := json.Unmarshal(data, &fc); err != nil {
			return nil, err
		}
		overlays := make([]Overlay, 0, len(fc.Features))
		for _, f := range fc.Features {
			overlays = append(overlays, Overlay{Geometry: f.Geometry, Properties: f.Properties})
		}
		return overlays, nil

	case "Feature":
		var f geom.GeoJSONFeature
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, err
		}
		return []Overlay{{Geometry: f.Geometry, Properties: f.Properties}}, nil

	default:
		g, err := geom.UnmarshalGeoJSON(data)
		if err != nil {
			return nil, err
		}
		return []Overlay{{Geometry: g}}, nil
	}
}

// OverlayFromWKT parses a WKT string into an Overlay.
func OverlayFromWKT(wkt string) (Overlay, error) {
	g, err := geom.UnmarshalWKT(wkt)
	if err != nil {
		return Overlay{}, err
	}
	return Overlay{Geometry: g}, nil
}

// OverlayFromWKB parses a WKB byte slice into an Overlay.
func OverlayFromWKB(wkb []byte) (Overlay, error) {
	g, err := geom.UnmarshalWKB(wkb)
	if err != nil {
		return Overlay{}, err
	}
	return Overlay{Geometry: g}, nil
}

// FitOverlaysBounds computes the center (lat, lng) and integer zoom so that
// the combined bounds of the overlays fit within a viewport of width x height
// logical pixels. MultiPolygons and collections are handled via their combined
// envelope.
func FitOverlaysBounds(overlays []Overlay, width, height float64) (lat, lng float64, zoom int, err error) {
	var env geom.Envelope
	found := false
	for _, o := range overlays {
		if o.Geometry.IsEmpty() {
			continue
		}
		e := o.Geometry.Envelope()
		if !found {
			env = e
			found = true
		} else {
			env = env.ExpandToIncludeEnvelope(e)
		}
	}
	if !found {
		return 0, 0, 0, fmt.Errorf("no non-empty overlays")
	}

	minXY, maxXY, ok := env.MinMaxXYs()
	if !ok {
		return 0, 0, 0, fmt.Errorf("overlay bounds are empty")
	}

	// Project the bounds into Web Mercator world fractions (0..1) at zoom 0.
	wm := carto.NewWebMercator(0)
	pMin := wm.Forward(geom.XY{X: minXY.X, Y: minXY.Y})
	pMax := wm.Forward(geom.XY{X: maxXY.X, Y: maxXY.Y})
	minX := math.Min(pMin.X, pMax.X)
	maxX := math.Max(pMin.X, pMax.X)
	minY := math.Min(pMin.Y, pMax.Y)
	maxY := math.Max(pMin.Y, pMax.Y)

	w := maxX - minX
	h := maxY - minY

	// Center: average in Mercator space, then reverse-project.
	cx := (minX + maxX) / 2
	cy := (minY + maxY) / 2
	center := wm.Reverse(geom.XY{X: cx, Y: cy})
	lat, lng = center.Y, center.X

	const margin = 0.9 // use 90% of the viewport
	if w <= 0 || h <= 0 {
		zoom = 15 // degenerate bounds (point or straight line)
	} else {
		zx := math.Log2((width * margin) / (w * TileSize))
		zy := math.Log2((height * margin) / (h * TileSize))
		zoom = int(math.Floor(math.Min(zx, zy)))
	}

	if zoom < 0 {
		zoom = 0
	}
	if zoom > 22 {
		zoom = 22
	}
	return lat, lng, zoom, nil
}

// strokeColor returns the resolved stroke color (explicit, from properties, or
// default red).
func (o Overlay) strokeColor() color.Color {
	if o.StrokeColor != nil {
		return o.StrokeColor
	}
	if s, ok := propString(o.Properties, "stroke", "stroke-color", "strokeColor"); ok {
		return parseColor(s)
	}
	return color.RGBA{255, 0, 0, 255}
}

// fillColor returns the resolved fill color (explicit, from properties, or
// transparent).
func (o Overlay) fillColor() color.Color {
	if o.FillColor != nil {
		return o.FillColor
	}
	if s, ok := propString(o.Properties, "fill", "fill-color", "fillColor"); ok {
		return parseColor(s)
	}
	return nil
}

func propString(props map[string]any, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := props[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s, true
			}
		}
	}
	return "", false
}

// projectGeometry reprojects a WGS84 geometry into screen pixel coordinates.
func projectGeometry(g geom.Geometry, wm *carto.WebMercator, minPxX, minPxY float64) geom.Geometry {
	return g.TransformXY(func(xy geom.XY) geom.XY {
		p := wm.Forward(geom.XY{X: xy.X, Y: xy.Y})
		return geom.XY{X: p.X*TileSize - minPxX, Y: p.Y*TileSize - minPxY}
	})
}

func drawOverlayGeometry(dc *canvas.Context, g geom.Geometry, stroke, fill color.Color, strokeWidth float64) {
	if g.IsEmpty() {
		return
	}
	switch {
	case g.IsGeometryCollection():
		for _, sub := range g.MustAsGeometryCollection().Dump() {
			drawOverlayGeometry(dc, sub, stroke, fill, strokeWidth)
		}
	case g.IsPolygon():
		drawOverlayPolygon(dc, g.MustAsPolygon(), stroke, fill, strokeWidth)
	case g.IsMultiPolygon():
		mp := g.MustAsMultiPolygon()
		for i := 0; i < mp.NumPolygons(); i++ {
			drawOverlayPolygon(dc, mp.PolygonN(i), stroke, fill, strokeWidth)
		}
	case g.IsLineString():
		drawOverlayLine(dc, g.MustAsLineString(), stroke, strokeWidth)
	case g.IsMultiLineString():
		mls := g.MustAsMultiLineString()
		for i := 0; i < mls.NumLineStrings(); i++ {
			drawOverlayLine(dc, mls.LineStringN(i), stroke, strokeWidth)
		}
	case g.IsPoint():
		drawOverlayPoint(dc, g.MustAsPoint(), stroke, fill, strokeWidth)
	case g.IsMultiPoint():
		mp := g.MustAsMultiPoint()
		for i := 0; i < mp.NumPoints(); i++ {
			drawOverlayPoint(dc, mp.PointN(i), stroke, fill, strokeWidth)
		}
	}
}

func drawOverlayPolygon(dc *canvas.Context, poly geom.Polygon, stroke, fill color.Color, strokeWidth float64) {
	if fill != nil {
		dc.SetFillColor(fill)
	}
	if stroke != nil {
		dc.SetStrokeColor(stroke)
		dc.SetStrokeWidth(strokeWidth)
	}

	rings := poly.DumpRings()
	for _, ring := range rings {
		seq := ring.Coordinates()
		for i := 0; i < seq.Length(); i++ {
			xy := seq.GetXY(i)
			if i == 0 {
				dc.MoveTo(xy.X, xy.Y)
			} else {
				dc.LineTo(xy.X, xy.Y)
			}
		}
		dc.Close()
	}

	switch {
	case fill != nil && stroke != nil:
		dc.FillStroke()
	case fill != nil:
		dc.Fill()
	case stroke != nil:
		dc.Stroke()
	}
}

func drawOverlayLine(dc *canvas.Context, ls geom.LineString, stroke color.Color, strokeWidth float64) {
	if stroke == nil {
		return
	}
	dc.SetStrokeColor(stroke)
	dc.SetStrokeWidth(strokeWidth)

	seq := ls.Coordinates()
	for i := 0; i < seq.Length(); i++ {
		xy := seq.GetXY(i)
		if i == 0 {
			dc.MoveTo(xy.X, xy.Y)
		} else {
			dc.LineTo(xy.X, xy.Y)
		}
	}
	dc.Stroke()
}

func drawOverlayPoint(dc *canvas.Context, pt geom.Point, stroke, fill color.Color, strokeWidth float64) {
	xy, ok := pt.XY()
	if !ok {
		return
	}
	c := fill
	if c == nil {
		c = stroke
	}
	if c == nil {
		c = color.RGBA{255, 0, 0, 255}
	}
	dc.SetFillColor(c)
	dc.DrawPath(xy.X, xy.Y, canvas.Circle(4))
	dc.Fill()
}
