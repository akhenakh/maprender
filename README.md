# maprender

Go library that renders Mapbox Vector Tiles into raster images using a Mapbox GL Style JSON specification.

## Features

- Fetches MVT tiles from any tile server with `{z}/{x}/{y}` URL templates
- Auto-resolves tile URL from style's vector source when `TileURLTemplate` is omitted
- Overzoom/underzoom: requests the closest available source zoom and scales when the client zoom exceeds the source's `maxzoom`/`minzoom`
- Handles gzip-compressed and raw tile responses
- Parses Mapbox GL Style JSON and applies paint properties per layer
- Zoom-dependent expressions: `interpolate`, `step`, `coalesce`, `match`, `get`
- Filter expressions: `==`, `!=`, `>`, `>=`, `<`, `<=`, `in`, `!in`, `has`, `!has`, `all`, `any`, `!`
- Renders `background`, `fill`, and `line` layer types
- All geometry types: Point, MultiPoint, LineString, MultiLineString, Polygon, MultiPolygon
- HiDPI/Retina rendering via configurable device pixel ratio
- Context cancellation support

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

## Dependencies

- [github.com/akhenakh/mvtgo](https://github.com/akhenakh/mvtgo) -- MVT Protobuf decoding
- [github.com/fogleman/gg](https://github.com/fogleman/gg) -- 2D drawing on image canvas
- [github.com/peterstace/simplefeatures](https://github.com/peterstace/simplefeatures) -- geometry types and Web Mercator projection

## License

MIT
