package maprender

import (
	"testing"

	"github.com/tdewolff/canvas"
)

func TestSplitFontStack(t *testing.T) {
	cases := []struct {
		name  string
		want  string
		style canvas.FontStyle
	}{
		{"Noto Sans Regular", "Noto Sans", canvas.FontRegular},
		{"Noto Sans Bold", "Noto Sans", canvas.FontBold},
		{"Noto Sans Italic", "Noto Sans", canvas.FontItalic},
		{"Open Sans SemiBold", "Open Sans", canvas.FontSemiBold},
		{"Roboto", "Roboto", canvas.FontRegular},
	}

	for _, tc := range cases {
		family, style := splitFontStack(tc.name)
		if family != tc.want || style != tc.style {
			t.Errorf("splitFontStack(%q) = %q, %v; want %q, %v", tc.name, family, style, tc.want, tc.style)
		}
	}
}
