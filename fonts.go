package maprender

import (
	"image/color"
	"strings"
	"sync"

	"github.com/tdewolff/canvas"
)

// ptPerPx converts a size expressed in pixels (of the logical coordinate
// system, where 1mm == 1 logical pixel) to typographic points, the unit that
// canvas.FontFamily.Face expects.
const ptPerPx = 72.0 / 25.4

// FontManager resolves Mapbox GL Style font stacks ("Noto Sans Regular",
// "Noto Sans Bold", ...) to canvas font faces backed by system fonts.
type FontManager struct {
	families map[string]*canvas.FontFamily
	def      *canvas.FontFamily
}

var (
	defaultFontsOnce sync.Once
	defaultFonts     *FontManager
)

// DefaultFonts returns a lazily-initialized, shared FontManager that loads the
// most common sans-serif fonts from the system.
func DefaultFonts() *FontManager {
	defaultFontsOnce.Do(func() {
		defaultFonts = NewFontManager()
	})
	return defaultFonts
}

// NewFontManager loads the given (or default) font families from the system.
// Any family that cannot be found is skipped.
func NewFontManager(families ...string) *FontManager {
	if len(families) == 0 {
		families = []string{"Noto Sans", "DejaVu Sans", "Liberation Sans", "Roboto", "Arial", "Helvetica"}
	}

	fm := &FontManager{families: map[string]*canvas.FontFamily{}}
	for _, name := range families {
		fam := canvas.NewFontFamily(name)
		loaded := false
		for _, style := range []canvas.FontStyle{
			canvas.FontRegular,
			canvas.FontBold,
			canvas.FontItalic,
			canvas.FontBold | canvas.FontItalic,
		} {
			if err := fam.LoadSystemFont(name, style); err == nil {
				loaded = true
			}
		}
		if loaded {
			fm.families[name] = fam
			if fm.def == nil {
				fm.def = fam
			}
		}
	}
	return fm
}

// splitFontStack splits a font stack name such as "Noto Sans Bold" into its
// family name and canvas font style.
func splitFontStack(name string) (string, canvas.FontStyle) {
	name = strings.TrimSpace(name)
	fields := strings.Fields(name)
	if len(fields) < 2 {
		return name, canvas.FontRegular
	}

	style := canvas.FontRegular
	switch strings.ToLower(fields[len(fields)-1]) {
	case "regular", "normal":
		style = canvas.FontRegular
	case "bold":
		style = canvas.FontBold
	case "italic", "oblique":
		style = canvas.FontItalic
	case "light":
		style = canvas.FontLight
	case "medium":
		style = canvas.FontMedium
	case "semibold":
		style = canvas.FontSemiBold
	default:
		return name, canvas.FontRegular
	}
	return strings.Join(fields[:len(fields)-1], " "), style
}

// Face returns a canvas font face for the given font stacks (in priority
// order). sizePx is the font size in logical pixels. When haloWidthPx is
// greater than zero, the face is decorated with a text halo (stroke) of the
// given color. It returns nil if no usable font family was loaded.
func (fm *FontManager) Face(fontStacks []string, sizePx float64, col color.Color, haloColor color.Color, haloWidthPx float64) *canvas.FontFace {
	fam := fm.def
	style := canvas.FontRegular
	for _, stack := range fontStacks {
		family, st := splitFontStack(stack)
		if f, ok := fm.families[family]; ok {
			fam = f
			style = st
			break
		}
	}
	if fam == nil {
		return nil
	}

	args := []any{col, style}
	if haloWidthPx > 0 {
		args = append(args, canvas.FontStroke(haloWidthPx, haloColor))
	}
	return fam.Face(sizePx*ptPerPx, args...)
}
