# maprender

Go library that renders Mapbox Vector Tiles into raster images using a Mapbox GL Style JSON specification.

## Features

- Fetches MVT tiles from any tile server with `{z}/{x}/{y}` URL templates
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

style, err := maprender.FetchStyle("https://example.com/style.json")
if err != nil {
    log.Fatal(err)
}

img, err := maprender.Render(ctx, maprender.RenderRequest{
    CenterLat:        40.7128,
    CenterLng:        -74.0060,
    Zoom:             14,
    Width:            800,
    Height:           600,
    DevicePixelRatio: 1.0,
    Style:            style,
    TileURLTemplate:  "https://tiles.example.com/{z}/{x}/{y}.pbf",
})
if err != nil {
    log.Fatal(err)
}
```

## Dependencies

- [github.com/akhenakh/mvtgo](https://github.com/akhenakh/mvtgo) -- MVT Protobuf decoding
- [github.com/fogleman/gg](https://github.com/fogleman/gg) -- 2D drawing on image canvas
- [github.com/peterstace/simplefeatures](https://github.com/peterstace/simplefeatures) -- geometry types and Web Mercator projection

## License

MIT
