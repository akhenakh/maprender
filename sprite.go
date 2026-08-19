package maprender

import (
	"encoding/json"
	"image"
	"image/draw"
	"image/png"
	"net/http"
	"sync"
)

// spriteIconMeta is the per-icon metadata from a Mapbox sprite JSON document.
type spriteIconMeta struct {
	X          int     `json:"x"`
	Y          int     `json:"y"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	PixelRatio float64 `json:"pixelRatio"`
}

// spriteIcon is a cropped sprite icon together with its nominal (logical) size.
type spriteIcon struct {
	img        image.Image
	pixelRatio float64
}

// Sprite is a Mapbox sprite: a set of named icons cropped from a sprite sheet.
type Sprite struct {
	icons map[string]spriteIcon
}

// FetchSprite loads a Mapbox sprite from the given base URL (the style's
// `sprite` field). It fetches `<url>.json` and `<url>.png`.
func FetchSprite(spriteURL string) (*Sprite, error) {
	resp, err := http.Get(spriteURL + ".json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var metas map[string]spriteIconMeta
	if err := json.NewDecoder(resp.Body).Decode(&metas); err != nil {
		return nil, err
	}

	resp, err = http.Get(spriteURL + ".png")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	sheet, err := png.Decode(resp.Body)
	if err != nil {
		return nil, err
	}

	s := &Sprite{icons: make(map[string]spriteIcon, len(metas))}
	for name, meta := range metas {
		pr := meta.PixelRatio
		if pr <= 0 {
			pr = 1
		}
		r := image.Rect(meta.X, meta.Y, meta.X+meta.Width, meta.Y+meta.Height)
		if !r.In(sheet.Bounds()) {
			continue
		}
		dst := image.NewRGBA(image.Rect(0, 0, meta.Width, meta.Height))
		draw.Draw(dst, dst.Bounds(), sheet, r.Min, draw.Src)
		s.icons[name] = spriteIcon{img: dst, pixelRatio: pr}
	}
	return s, nil
}

// Icon returns the cropped image and pixel ratio for the named icon.
func (s *Sprite) Icon(name string) (image.Image, float64, bool) {
	if s == nil {
		return nil, 0, false
	}
	ic, ok := s.icons[name]
	if !ok {
		return nil, 0, false
	}
	return ic.img, ic.pixelRatio, true
}

var (
	spriteCacheMu sync.Mutex
	spriteCache   = map[string]*Sprite{}
)

// fetchSpriteCached returns the sprite for url, reusing a cached copy when
// available.
func fetchSpriteCached(url string) (*Sprite, error) {
	spriteCacheMu.Lock()
	defer spriteCacheMu.Unlock()
	if s, ok := spriteCache[url]; ok {
		return s, nil
	}
	s, err := FetchSprite(url)
	if err != nil {
		return nil, err
	}
	spriteCache[url] = s
	return s, nil
}
