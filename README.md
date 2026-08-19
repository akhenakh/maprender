# maprender

Go library that renders Mapbox Vector Tiles into raster images using a Mapbox GL Style JSON specification.

## Features

- Fetches MVT tiles from any tile server with `{z}/{x}/{y}` URL templates
- Auto-resolves tile URL from style's vector source when `TileURLTemplate` is omitted
- Overzoom/underzoom: requests the closest available source zoom and scales when the client zoom exceeds the source's `maxzoom`/`minzoom`
- Handles gzip-compressed and raw tile responses
- Parses Mapbox GL Style JSON and applies paint properties per layer
- Zoom-dependent expressions: `interpolate`, `step`, `coalesce`, `match`, `get`
- Data expressions: `case`, `concat`, `to-string`, `to-number`, `literal`, ...
- Filter expressions: `==`, `!=`, `>`, `>=`, `<`, `<=`, `in`, `!in`, `has`, `!has`, `all`, `any`, `!`
- Renders `background`, `fill`, `line`, and `symbol` (text) layer types
- Text labels rendered from system fonts, with halo and collision detection
- Sprite icons rendered alongside labels (POIs, shields, one-way arrows, ...)
- All geometry types: Point, MultiPoint, LineString, MultiLineString, Polygon, MultiPolygon
- HiDPI/Retina rendering via configurable device pixel ratio
- Context cancellation support
- Multiple output formats via `RenderCanvas` (PNG, SVG, PDF, EPS, ...)

## Usage

```go
import "github.com/akhenakh/maprender"

style, err := maprender.FetchStyle("https://tiles.openfreemap.org/styles/liberty")
if err != nil {
    log.Fatal(err)
}

img, err := maprender.Render(ctx, maprender.RenderRequest{
    CenterLat:        48.864716,
    CenterLng:        2.349014,
    Zoom:             14,
    Width:            800,
    Height:           600,
    DevicePixelRatio: 1.0,
    Style:            style,
})
if err != nil {
    log.Fatal(err)
}
```

`TileURLTemplate` is optional — when omitted, it is automatically resolved from the style's vector source TileJSON. You can also set it explicitly:

```go
TileURLTemplate: "https://tiles.openfreemap.org/planet/20260422_001001_pt/{z}/{x}/{y}.pbf",
```

When the requested `Zoom` is higher (or lower) than the source's available range, the renderer automatically fetches the closest available zoom and scales it (`overzoom`/`underzoom`). If you set `TileURLTemplate` manually, the source range is unknown; provide it via `SourceMinZoom`/`SourceMaxZoom` to enable the same behavior:

```go
SourceMaxZoom: 14,
```

### Text labels, fonts and icons

`symbol` layers are rendered as text. The renderer loads the fonts referenced by the style's `text-font` stacks (e.g. `"Noto Sans Bold"`) from the operating system and falls back to the first available family. A default `FontManager` is used automatically; you can customise the families or provide your own:

```go
fonts := maprender.NewFontManager("Noto Sans", "DejaVu Sans")

req := maprender.RenderRequest{
    // ...
    Fonts: fonts,
}
```

Sprite icons (the style's `sprite` field, e.g. POI markers and road shields) are fetched automatically from `<sprite>.json` and `<sprite>.png` and drawn next to their labels. You can override the sprite via `RenderRequest.Sprite`, loaded with `maprender.FetchSprite`:

```go
sprite, err := maprender.FetchSprite("https://tiles.openfreemap.org/sprites/ofm_f384/ofm")

req := maprender.RenderRequest{
    // ...
    Sprite: sprite,
}
```

### Overlays

Draw arbitrary geometries (WGS84 lon/lat) on top of the map from GeoJSON, WKT, WKB, or a `geom.Geometry` directly. The stroke defaults to red and the fill to transparent; both can be set explicitly or derived from GeoJSON feature properties (keys `stroke`/`stroke-color`/`strokeColor` and `fill`/`fill-color`/`fillColor`):

```go
// GeoJSON (a Feature or FeatureCollection; properties drive colors)
overlays, err := maprender.OverlayFromGeoJSON([]byte(`{
    "type": "Feature",
    "properties": {"fill": "#ff000080", "stroke": "#ff0000"},
    "geometry": {"type": "Polygon", "coordinates": [[[2.34,48.85],[2.36,48.85],[2.36,48.87],[2.34,48.87],[2.34,48.85]]]}
}`))

// or WKT / WKB / a geometry
overlay, err := maprender.OverlayFromWKT("LINESTRING(2.33 48.86, 2.37 48.86)")

req := maprender.RenderRequest{
    // ...
    Overlays:   overlays,
    FitOverlays: true, // compute center/zoom from the overlays' combined bounds
}
```

### Output formats

`Render` returns a raster `*image.RGBA` (ready to encode as PNG). For other formats, use `RenderCanvas` to obtain a vector `*canvas.Canvas`, then render it with any of the canvas writers (`github.com/tdewolff/canvas/renderers/...`): `svg`, `pdf`, `ps`, `eps`, `png`, `jpeg`, `gif`, `tiff`, `bmp`, `webp`, ...

```go
import (
    "os"
    "github.com/tdewolff/canvas/renderers/pdf"
    "github.com/tdewolff/canvas/renderers/svg"
)

c, err := maprender.RenderCanvas(ctx, req)

// SVG
f, _ := os.Create("/tmp/map.svg")
w := svg.New(f, c.W, c.H, nil)
c.RenderTo(w)
w.Close()
f.Close()

// PDF
f, _ = os.Create("/tmp/map.pdf")
p := pdf.New(f, c.W, c.H, nil)
c.RenderTo(p)
p.Close()
f.Close()
```

The `cmd/example` program demonstrates this: pass `-svg` to write `output.svg` instead of the default `output.png`:

```sh
go run ./cmd/example        # writes output.png
go run ./cmd/example -svg   # writes output.svg
```

## Dependencies

- [github.com/akhenakh/mvtgo](https://github.com/akhenakh/mvtgo) -- MVT Protobuf decoding
- [github.com/tdewolff/canvas](https://github.com/tdewolff/canvas) -- 2D vector drawing (raster, SVG, PDF, EPS, ...) and text rendering
- [github.com/peterstace/simplefeatures](https://github.com/peterstace/simplefeatures) -- geometry types and Web Mercator projection

## License

MIT
