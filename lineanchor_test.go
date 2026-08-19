package maprender

import (
	"math"
	"testing"

	"github.com/peterstace/simplefeatures/geom"
)

func TestLineAnchor(t *testing.T) {
	cases := []struct {
		name      string
		xys       []float64
		wantX     float64
		wantY     float64
		wantAngle float64
	}{
		{"horizontal", []float64{0, 0, 10, 0}, 5, 0, 0},
		{"vertical-down", []float64{0, 0, 0, 10}, 0, 5, 90},
		{"vertical-up", []float64{0, 10, 0, 0}, 0, 5, -90},
		{"diagonal-down-right", []float64{0, 0, 10, 10}, 5, 5, 45},
		{"diagonal-up-right", []float64{0, 10, 10, 0}, 5, 5, -45},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ls := geom.NewLineStringXY(tc.xys...)
			xy, angle, ok := lineAnchor(ls)
			if !ok {
				t.Fatalf("lineAnchor() ok = false")
			}
			if math.Abs(xy.X-tc.wantX) > 1e-9 || math.Abs(xy.Y-tc.wantY) > 1e-9 {
				t.Errorf("lineAnchor() point = (%v, %v); want (%v, %v)", xy.X, xy.Y, tc.wantX, tc.wantY)
			}
			if math.Abs(angle-tc.wantAngle) > 1e-9 {
				t.Errorf("lineAnchor() angle = %v; want %v", angle, tc.wantAngle)
			}
		})
	}
}
