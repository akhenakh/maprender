package maprender

import (
	"encoding/json"
	"image/color"
	"testing"

	"github.com/peterstace/simplefeatures/geom"
)

func TestParseColor(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  color.RGBA
	}{
		{"Hex 3", "#f00", color.RGBA{255, 0, 0, 255}},
		{"Hex 6", "#00ff00", color.RGBA{0, 255, 0, 255}},
		{"RGB", "rgb(0, 0, 255)", color.RGBA{0, 0, 255, 255}},
		{"RGBA", "rgba(255, 255, 255, 0.5)", color.RGBA{255, 255, 255, 127}},
		{"HSL", "hsl(120, 100%, 50%)", color.RGBA{0, 255, 0, 255}},
		{"HSLA", "hsla(240, 100%, 50%, 0.2)", color.RGBA{0, 0, 255, 51}},
		{"Invalid Type", 123, color.RGBA{0, 0, 0, 0}},
		{"Invalid String", "not a color", color.RGBA{0, 0, 0, 0}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseColor(tc.input)
			if got != tc.want {
				t.Errorf("parseColor(%v) = %v; want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestToFloat(t *testing.T) {
	cases := []struct {
		input any
		want  float64
		ok    bool
	}{
		{42.5, 42.5, true},
		{42, 42.0, true},
		{json.Number("42.5"), 42.5, true},
		{"42.5", 0, false},
	}

	for _, tc := range cases {
		t.Run("toFloat", func(t *testing.T) {
			got, ok := toFloat(tc.input)
			if ok != tc.ok || got != tc.want {
				t.Errorf("toFloat(%v) = %v, %v; want %v, %v", tc.input, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestEvalInterpolate(t *testing.T) {
	expr := []any{
		"interpolate",
		[]any{"linear"},
		[]any{"zoom"},
		0.0, 0.0,
		10.0, 100.0,
	}

	cases := []struct {
		zoom float64
		want float64
	}{
		{-1, 0},   // Below min, clamp to first
		{0, 0},    // Exact min
		{5, 50},   // Halfway
		{10, 100}, // Exact max
		{15, 100}, // Above max, clamp to last
	}

	for _, tc := range cases {
		t.Run("zoom", func(t *testing.T) {
			got := evalInterpolate(expr, tc.zoom)
			if got != tc.want {
				t.Errorf("evalInterpolate zoom=%v = %v; want %v", tc.zoom, got, tc.want)
			}
		})
	}
}

func TestEvalStep(t *testing.T) {
	expr := []any{
		"step",
		[]any{"zoom"},
		0.0, // base value
		5.0, 10.0,
		10.0, 20.0,
	}

	cases := []struct {
		zoom float64
		want float64
	}{
		{4, 0},
		{5, 10},
		{9, 10},
		{10, 20},
		{15, 20},
	}

	for _, tc := range cases {
		t.Run("step", func(t *testing.T) {
			got := evalStep(expr, tc.zoom)
			if got != tc.want {
				t.Errorf("evalStep zoom=%v = %v; want %v", tc.zoom, got, tc.want)
			}
		})
	}
}

func TestEvaluateFilter(t *testing.T) {
	geomPoint := geom.NewPoint(geom.Coordinates{XY: geom.XY{X: 0, Y: 0}}).AsGeometry()
	props := map[string]any{
		"class": "park",
		"rank":  2,
		"valid": true,
	}

	cases := []struct {
		name   string
		filter []any
		want   bool
	}{
		{"Empty filter", []any{}, true},
		{"Equality true", []any{"==", "class", "park"}, true},
		{"Equality false", []any{"==", "class", "water"}, false},
		{"Not equal true", []any{"!=", "class", "water"}, true},
		{"Not equal false", []any{"!=", "class", "park"}, false},
		{"Greater than true", []any{">", "rank", 1}, true},
		{"Less than false", []any{"<", "rank", 2}, false},
		{"Less eq true", []any{"<=", "rank", 2}, true},
		{"Has true", []any{"has", "class"}, true},
		{"Has false", []any{"has", "name"}, false},
		{"!Has true", []any{"!has", "name"}, true},
		{"In true", []any{"in", "class", "water", "park"}, true},
		{"In false", []any{"in", "class", "water", "road"}, false},
		{"All true", []any{"all", []any{"==", "class", "park"}, []any{">", "rank", 1}}, true},
		{"All false", []any{"all", []any{"==", "class", "park"}, []any{">", "rank", 5}}, false},
		{"Any true", []any{"any", []any{"==", "class", "water"}, []any{"==", "class", "park"}}, true},
		{"Not true", []any{"!", []any{"==", "class", "water"}}, true},
		{"Geometry type point", []any{"==", "$type", "Point"}, false}, // Test using geometry-type instead
		{"Geometry type valid", []any{"==", []any{"geometry-type"}, "Point"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateFilter(tc.filter, props, geomPoint)
			if got != tc.want {
				t.Errorf("evaluateFilter() = %v; want %v", got, tc.want)
			}
		})
	}
}

func TestGetLayerByID(t *testing.T) {
	style := &MapStyle{
		Layers: []StyleLayer{
			{ID: "background"},
			{ID: "water"},
		},
	}

	layer := GetLayerByID(style, "water")
	if layer == nil || layer.ID != "water" {
		t.Errorf("expected to find layer 'water', got %v", layer)
	}

	layer = GetLayerByID(style, "missing")
	if layer != nil {
		t.Errorf("expected nil for missing layer, got %v", layer)
	}
}
