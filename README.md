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

### Text labels and fonts

`symbol` layers are rendered as text. The renderer loads the fonts referenced by the style's `text-font` stacks (e.g. `"Noto Sans Bold"`) from the operating system and falls back to the first available family. A default `FontManager` is used automatically; you can customise the families or provide your own:

```go
fonts := maprender.NewFontManager("Noto Sans", "DejaVu Sans")

req := maprender.RenderRequest{
    // ...
    Fonts: fonts,
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
