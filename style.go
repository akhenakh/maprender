package maprender

import (
	"encoding/json"
	"fmt"
	"image/color"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/peterstace/simplefeatures/geom"
)

type StyleLayer struct {
	ID          string      `json:"id"`
	Type        string      `json:"type"`
	SourceLayer string      `json:"source-layer"`
	Paint       PaintProps  `json:"paint"`
	Layout      LayoutProps `json:"layout"`
	Filter      []any       `json:"filter"`
	MinZoom     *float64    `json:"minzoom"`
	MaxZoom     *float64    `json:"maxzoom"`
}

type PaintProps struct {
	BackgroundColor any `json:"background-color"`
	FillColor       any `json:"fill-color"`
	FillOpacity     any `json:"fill-opacity"`
	LineColor       any `json:"line-color"`
	LineWidth       any `json:"line-width"`
	LineOpacity     any `json:"line-opacity"`
	LineDashArray   any `json:"line-dasharray"`
	TextColor       any `json:"text-color"`
	TextHaloColor   any `json:"text-halo-color"`
	TextHaloWidth   any `json:"text-halo-width"`
	TextOpacity     any `json:"text-opacity"`
}

type LayoutProps struct {
	TextField     any      `json:"text-field"`
	TextFont      []string `json:"text-font"`
	TextSize      any      `json:"text-size"`
	TextAnchor    any      `json:"text-anchor"`
	TextTransform any      `json:"text-transform"`
	IconImage     any      `json:"icon-image"`
	IconSize      any      `json:"icon-size"`
	IconAnchor    any      `json:"icon-anchor"`
}

type MapStyle struct {
	Layers    []StyleLayer `json:"layers"`
	SourceURL string
	SpriteURL string
	GlyphsURL string
}

type TileJSON struct {
	Tiles    []string `json:"tiles"`
	MinZoom  int      `json:"minzoom"`
	MaxZoom  int      `json:"maxzoom"`
	TileSize int      `json:"tileSize"`
}

func FetchStyle(styleURL string) (*MapStyle, error) {
	resp, err := http.Get(styleURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw struct {
		Layers  []StyleLayer `json:"layers"`
		Sprite  string       `json:"sprite"`
		Glyphs  string       `json:"glyphs"`
		Sources map[string]struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"sources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	renderable := make([]StyleLayer, 0, len(raw.Layers))
	for _, l := range raw.Layers {
		switch l.Type {
		case "background", "fill", "line", "symbol":
			renderable = append(renderable, l)
		}
	}

	var sourceURL string
	for _, src := range raw.Sources {
		if src.Type == "vector" && src.URL != "" {
			sourceURL = src.URL
			break
		}
	}

	return &MapStyle{Layers: renderable, SourceURL: sourceURL, SpriteURL: raw.Sprite, GlyphsURL: raw.Glyphs}, nil
}

func FetchTileJSON(sourceURL string) (*TileJSON, error) {
	resp, err := http.Get(sourceURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var tj TileJSON
	if err := json.NewDecoder(resp.Body).Decode(&tj); err != nil {
		return nil, err
	}
	if len(tj.Tiles) == 0 {
		return nil, fmt.Errorf("TileJSON contains no tile URLs")
	}

	return &tj, nil
}

func FetchTileURLTemplate(sourceURL string) (string, error) {
	tj, err := FetchTileJSON(sourceURL)
	if err != nil {
		return "", err
	}
	return tj.Tiles[0], nil
}

func (s *MapStyle) ResolveTileJSON() (*TileJSON, error) {
	if s.SourceURL == "" {
		return nil, fmt.Errorf("style has no vector source URL")
	}
	return FetchTileJSON(s.SourceURL)
}

func (s *MapStyle) ResolveTileURL() (string, error) {
	tj, err := s.ResolveTileJSON()
	if err != nil {
		return "", err
	}
	return tj.Tiles[0], nil
}

func evalExpr(expr any, zoom float64) any {
	arr, ok := expr.([]any)
	if !ok {
		return expr
	}
	if len(arr) == 0 {
		return expr
	}

	switch arr[0] {
	case "interpolate":
		return evalInterpolate(arr, zoom)
	case "coalesce":
		for _, v := range arr[1:] {
			r := evalExpr(v, zoom)
			if r != nil {
				return r
			}
		}
		return nil
	case "get":
		return nil
	case "match":
		return arr[len(arr)-1]
	case "step":
		return evalStep(arr, zoom)
	default:
		return nil
	}
}

func evalInterpolate(arr []any, zoom float64) any {
	if len(arr) < 5 {
		return nil
	}
	_, ok := arr[2].([]any)
	if !ok {
		return nil
	}
	zoomExpr := arr[2]
	if ze, ok := zoomExpr.([]any); !ok || len(ze) < 1 || ze[0] != "zoom" {
		return nil
	}

	type kv struct {
		z float64
		v any
	}
	var pairs []kv
	for i := 3; i < len(arr)-1; i += 2 {
		zf, _ := toFloat(arr[i])
		pairs = append(pairs, kv{zf, arr[i+1]})
	}
	if len(pairs) < 2 {
		return pairs[0].v
	}

	if zoom <= pairs[0].z {
		return pairs[0].v
	}
	if zoom >= pairs[len(pairs)-1].z {
		return pairs[len(pairs)-1].v
	}

	for i := 0; i < len(pairs)-1; i++ {
		if zoom >= pairs[i].z && zoom <= pairs[i+1].z {
			z1, v1 := pairs[i].z, pairs[i].v
			z2, v2 := pairs[i+1].z, pairs[i+1].v
			v1f, ok1 := toFloat(v1)
			v2f, ok2 := toFloat(v2)
			if ok1 && ok2 && z2 != z1 {
				t := (zoom - z1) / (z2 - z1)
				return v1f + t*(v2f-v1f)
			}
			return v1
		}
	}
	return pairs[len(pairs)-1].v
}

func evalStep(arr []any, zoom float64) any {
	if len(arr) < 4 {
		return nil
	}
	zoomExpr := arr[1]
	if ze, ok := zoomExpr.([]any); !ok || len(ze) < 1 || ze[0] != "zoom" {
		return nil
	}
	output := arr[2]
	for i := 3; i < len(arr)-1; i += 2 {
		zf, _ := toFloat(arr[i])
		if zoom < zf {
			return output
		}
		output = arr[i+1]
	}
	return output
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func resolvePaintValue(val any, zoom float64) any {
	return evalExpr(val, zoom)
}

func resolveLineWidth(val any, zoom float64) float64 {
	if val == nil {
		return 1.0
	}
	r := resolvePaintValue(val, zoom)
	if f, ok := toFloat(r); ok {
		return f
	}
	return 1.0
}

func resolveTextSize(val any, zoom float64) float64 {
	if val == nil {
		return 16.0
	}
	r := resolvePaintValue(val, zoom)
	if f, ok := toFloat(r); ok {
		return f
	}
	return 16.0
}

func resolveIconSize(val any, zoom float64) float64 {
	if val == nil {
		return 1.0
	}
	r := resolvePaintValue(val, zoom)
	if f, ok := toFloat(r); ok {
		return f
	}
	return 1.0
}

// resolveIconImage evaluates an icon-image expression to an icon name. It
// supports zoom-based "step"/"interpolate" expressions as well as the
// property-based expressions handled by evalDataExpr.
func resolveIconImage(expr any, props map[string]any, geometry geom.Geometry, zoom float64) string {
	if expr == nil {
		return ""
	}
	if arr, ok := expr.([]any); ok && len(arr) > 0 {
		if op, ok := arr[0].(string); ok {
			switch op {
			case "step":
				return toString(evalStep(arr, zoom))
			case "interpolate":
				return toString(evalInterpolate(arr, zoom))
			}
		}
	}
	return toString(evalDataExpr(expr, props, geometry))
}

// evalDataExpr evaluates a Mapbox data-driven expression against feature
// properties and geometry. It is a superset of the operators supported by
// evalFilterExpr and additionally handles "case", "concat", "to-string",
// "to-number" and "literal".
func evalDataExpr(expr any, props map[string]any, geometry geom.Geometry) any {
	arr, ok := expr.([]any)
	if !ok {
		return expr
	}
	if len(arr) == 0 {
		return nil
	}

	op, ok := arr[0].(string)
	if !ok {
		return nil
	}

	switch op {
	case "get":
		if len(arr) == 2 {
			if key, ok := arr[1].(string); ok {
				if key == "geometry-type" {
					return geometryTypeName(geometry)
				}
				if v, has := props[key]; has {
					return v
				}
			}
		}
		return nil

	case "has":
		if len(arr) == 2 {
			_, ok := props[resolveGetKey(arr[1])]
			return ok
		}
		return false

	case "coalesce":
		for _, v := range arr[1:] {
			if r := evalDataExpr(v, props, geometry); r != nil {
				return r
			}
		}
		return nil

	case "concat":
		var sb strings.Builder
		for _, v := range arr[1:] {
			sb.WriteString(toString(evalDataExpr(v, props, geometry)))
		}
		return sb.String()

	case "to-string":
		if len(arr) == 2 {
			return toString(evalDataExpr(arr[1], props, geometry))
		}
		return ""

	case "to-number":
		if len(arr) == 2 {
			if f, ok := toFloat(evalDataExpr(arr[1], props, geometry)); ok {
				return f
			}
		}
		return 0.0

	case "case":
		return evalCase(arr, props, geometry)

	case "literal":
		if len(arr) == 2 {
			return arr[1]
		}
		return nil

	default:
		return evalFilterExpr(expr, props, geometry)
	}
}

func evalCase(arr []any, props map[string]any, geometry geom.Geometry) any {
	if len(arr) < 3 {
		return nil
	}
	for i := 1; i+1 < len(arr); i += 2 {
		if b, ok := evalDataExpr(arr[i], props, geometry).(bool); ok && b {
			return evalDataExpr(arr[i+1], props, geometry)
		}
	}
	return evalDataExpr(arr[len(arr)-1], props, geometry)
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func resolveTextField(expr any, props map[string]any, geometry geom.Geometry) string {
	return toString(evalDataExpr(expr, props, geometry))
}

func GetLayerByID(style *MapStyle, id string) *StyleLayer {
	for _, l := range style.Layers {
		if l.ID == id {
			return &l
		}
	}
	return nil
}

func evaluateFilter(filter []any, props map[string]any, geometry geom.Geometry) bool {
	if len(filter) == 0 {
		return true
	}
	result := evalFilterExpr(filter, props, geometry)
	if b, ok := result.(bool); ok {
		return b
	}
	return true
}

func geometryTypeName(g geom.Geometry) string {
	if g.IsPoint() {
		return "Point"
	}
	if g.IsMultiPoint() {
		return "MultiPoint"
	}
	if g.IsLineString() {
		return "LineString"
	}
	if g.IsMultiLineString() {
		return "MultiLineString"
	}
	if g.IsPolygon() {
		return "Polygon"
	}
	if g.IsMultiPolygon() {
		return "MultiPolygon"
	}
	if g.IsGeometryCollection() {
		return "GeometryCollection"
	}
	return ""
}

func evalFilterExpr(expr any, props map[string]any, geometry geom.Geometry) any {
	arr, ok := expr.([]any)
	if !ok {
		if s, ok := expr.(string); ok {
			if v, has := props[s]; has {
				return v
			}
		}
		return expr
	}
	if len(arr) == 0 {
		return expr
	}

	op, ok := arr[0].(string)
	if !ok {
		return true
	}

	switch op {
	case "get":
		if len(arr) == 2 {
			if key, ok := arr[1].(string); ok {
				if key == "geometry-type" {
					return geometryTypeName(geometry)
				}
				if v, has := props[key]; has {
					return v
				}
			}
		}
		return nil

	case "geometry-type":
		return geometryTypeName(geometry)

	case "match":
		return evalMatch(arr, props, geometry)

	case "coalesce":
		for _, v := range arr[1:] {
			r := evalFilterExpr(v, props, geometry)
			if r != nil {
				return r
			}
		}
		return nil

	case "==":
		if len(arr) == 3 {
			lhs := evalFilterExpr(arr[1], props, geometry)
			rhs := evalFilterExpr(arr[2], props, geometry)
			return valuesEqual(lhs, rhs)
		}
		return true

	case "!=":
		if len(arr) == 3 {
			lhs := evalFilterExpr(arr[1], props, geometry)
			rhs := evalFilterExpr(arr[2], props, geometry)
			return !valuesEqual(lhs, rhs)
		}
		return true

	case ">":
		if len(arr) == 3 {
			lf, ok1 := toFloat(evalFilterExpr(arr[1], props, geometry))
			rf, ok2 := toFloat(evalFilterExpr(arr[2], props, geometry))
			if ok1 && ok2 {
				return lf > rf
			}
		}
		return true

	case ">=":
		if len(arr) == 3 {
			lf, ok1 := toFloat(evalFilterExpr(arr[1], props, geometry))
			rf, ok2 := toFloat(evalFilterExpr(arr[2], props, geometry))
			if ok1 && ok2 {
				return lf >= rf
			}
		}
		return true

	case "<":
		if len(arr) == 3 {
			lf, ok1 := toFloat(evalFilterExpr(arr[1], props, geometry))
			rf, ok2 := toFloat(evalFilterExpr(arr[2], props, geometry))
			if ok1 && ok2 {
				return lf < rf
			}
		}
		return true

	case "<=":
		if len(arr) == 3 {
			lf, ok1 := toFloat(evalFilterExpr(arr[1], props, geometry))
			rf, ok2 := toFloat(evalFilterExpr(arr[2], props, geometry))
			if ok1 && ok2 {
				return lf <= rf
			}
		}
		return true

	case "in":
		if len(arr) >= 3 {
			lhs := evalFilterExpr(arr[1], props, geometry)
			for _, v := range arr[2:] {
				if valuesEqual(evalFilterExpr(v, props, geometry), lhs) {
					return true
				}
			}
			return false
		}

	case "!in":
		if len(arr) >= 3 {
			lhs := evalFilterExpr(arr[1], props, geometry)
			for _, v := range arr[2:] {
				if valuesEqual(evalFilterExpr(v, props, geometry), lhs) {
					return false
				}
			}
			return true
		}

	case "has":
		if len(arr) == 2 {
			key := resolveGetKey(arr[1])
			_, ok := props[key]
			return ok
		}

	case "!has":
		if len(arr) == 2 {
			key := resolveGetKey(arr[1])
			_, ok := props[key]
			return !ok
		}

	case "all":
		for _, f := range arr[1:] {
			r := evalFilterExpr(f, props, geometry)
			if b, ok := r.(bool); ok && !b {
				return false
			}
		}
		return true

	case "any":
		for _, f := range arr[1:] {
			r := evalFilterExpr(f, props, geometry)
			if b, ok := r.(bool); ok && b {
				return true
			}
		}
		return false

	case "!":
		if len(arr) == 2 {
			r := evalFilterExpr(arr[1], props, geometry)
			if b, ok := r.(bool); ok {
				return !b
			}
		}
	}

	return true
}

func evalMatch(arr []any, props map[string]any, geometry geom.Geometry) any {
	if len(arr) < 4 {
		return evalDataExpr(arr[len(arr)-1], props, geometry)
	}
	input := evalFilterExpr(arr[1], props, geometry)

	for i := 2; i < len(arr)-1; i += 2 {
		labels := arr[i]
		output := arr[i+1]

		switch l := labels.(type) {
		case []any:
			for _, label := range l {
				if valuesEqual(evalFilterExpr(label, props, geometry), input) {
					return evalDataExpr(output, props, geometry)
				}
			}
		default:
			if valuesEqual(evalFilterExpr(labels, props, geometry), input) {
				return evalDataExpr(output, props, geometry)
			}
		}
	}
	return evalDataExpr(arr[len(arr)-1], props, geometry)
}

// valuesEqual compares two expression values for equality without the
// allocation cost of formatting both values as strings. It prefers numeric and
// string comparison and only falls back to string formatting for mixed types.
func valuesEqual(a, b any) bool {
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			return af == bf
		}
	}
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			return as == bs
		}
	}
	if ab, ok := a.(bool); ok {
		if bb, ok := b.(bool); ok {
			return ab == bb
		}
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func resolveGetKey(key any) string {
	if s, ok := key.(string); ok {
		return s
	}
	if arr, ok := key.([]any); ok && len(arr) == 2 {
		if arr[0] == "get" {
			if s, ok := arr[1].(string); ok {
				return s
			}
		}
	}
	return ""
}

func parseColor(val any) color.Color {
	cStr, ok := val.(string)
	if !ok {
		return color.RGBA{0, 0, 0, 0}
	}

	cStr = strings.TrimSpace(cStr)

	// Hex (#RGB, #RGBA, #RRGGBB, or #RRGGBBAA)
	if after, ok0 := strings.CutPrefix(cStr, "#"); ok0 {
		hex := after
		switch len(hex) {
		case 3:
			hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
		case 4:
			hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2], hex[3], hex[3]})
		}
		if len(hex) == 6 || len(hex) == 8 {
			r, _ := strconv.ParseUint(hex[0:2], 16, 8)
			g, _ := strconv.ParseUint(hex[2:4], 16, 8)
			b, _ := strconv.ParseUint(hex[4:6], 16, 8)
			a := uint64(255)
			if len(hex) == 8 {
				a, _ = strconv.ParseUint(hex[6:8], 16, 8)
			}
			return color.RGBA{uint8(r), uint8(g), uint8(b), uint8(a)}
		}
	}

	// rgba(r, g, b, a) or rgb(r, g, b)
	if strings.HasPrefix(cStr, "rgb") {
		re := regexp.MustCompile(`rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)(?:\s*,\s*([\d.]+))?\s*\)`)
		matches := re.FindStringSubmatch(cStr)
		if len(matches) >= 4 {
			r, _ := strconv.ParseUint(matches[1], 10, 8)
			g, _ := strconv.ParseUint(matches[2], 10, 8)
			b, _ := strconv.ParseUint(matches[3], 10, 8)
			a := uint8(255)
			if len(matches) == 5 && matches[4] != "" {
				af, _ := strconv.ParseFloat(matches[4], 64)
				a = uint8(af * 255)
			}
			return color.RGBA{uint8(r), uint8(g), uint8(b), a}
		}
	}

	// hsla(h, s%, l%, a) or hsl(h, s%, l%)
	if strings.HasPrefix(cStr, "hsl") {
		re := regexp.MustCompile(`hsla?\(\s*(\d+)\s*,\s*([\d.]+)%\s*,\s*([\d.]+)%(?:\s*,\s*([\d.]+))?\s*\)`)
		matches := re.FindStringSubmatch(cStr)
		if len(matches) >= 4 {
			h, _ := strconv.ParseFloat(matches[1], 64)
			s, _ := strconv.ParseFloat(matches[2], 64)
			l, _ := strconv.ParseFloat(matches[3], 64)
			a := 1.0
			if len(matches) == 5 && matches[4] != "" {
				a, _ = strconv.ParseFloat(matches[4], 64)
			}
			return hslToRGBA(h, s, l, a)
		}
	}

	return color.RGBA{0, 0, 0, 0}
}

func hslToRGBA(h, s, l, a float64) color.RGBA {
	s /= 100
	l /= 100
	var r, g, b float64
	if s == 0 {
		r, g, b = l, l, l
	} else {
		var q float64
		if l < 0.5 {
			q = l * (1 + s)
		} else {
			q = l + s - l*s
		}
		p := 2*l - q
		r = hueToRGB(p, q, h/360+1.0/3)
		g = hueToRGB(p, q, h/360)
		b = hueToRGB(p, q, h/360-1.0/3)
	}
	return color.RGBA{uint8(r * 255), uint8(g * 255), uint8(b * 255), uint8(a * 255)}
}

func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t += 1
	}
	if t > 1 {
		t -= 1
	}
	if t < 1.0/6 {
		return p + (q-p)*6*t
	}
	if t < 1.0/2 {
		return q
	}
	if t < 2.0/3 {
		return p + (q-p)*(2.0/3-t)*6
	}
	return p
}
