package maprender

import (
	"encoding/binary"
)

// Minimal MVT (Mapbox Vector Tile) protobuf encoding helpers for tests.

func putVarint(b []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append(b, buf[:n]...)
}

func putField(b []byte, field int, wire int) []byte {
	return putVarint(b, uint64(field)<<3|uint64(wire))
}

func putString(b []byte, field int, s string) []byte {
	b = putField(b, field, 2)
	b = putVarint(b, uint64(len(s)))
	return append(b, s...)
}

func putBytesField(b []byte, field int, v []byte) []byte {
	b = putField(b, field, 2)
	b = putVarint(b, uint64(len(v)))
	return append(b, v...)
}

func zigzag(v int32) uint64 { return uint64((uint32(v) << 1) ^ uint32(v>>31)) }

// encodeMVTPointTile builds a tile with a single layer containing point
// features at the given tile coordinates (in extent units), each carrying one
// property: keys[0] -> values[0].
func encodeMVTPointTile(layerName string, extent uint32, pts [][2]int32, key string, val string) []byte {
	var valueMsg []byte
	valueMsg = putString(valueMsg, 1, val) // string_value

	var feats [][]byte
	for _, pt := range pts {
		var f []byte
		var tags []byte
		tags = putVarint(tags, 0) // key index 0
		tags = putVarint(tags, 0) // value index 0
		f = putBytesField(f, 2, tags)

		f = putField(f, 3, 0)
		f = putVarint(f, 1) // type POINT

		var geo []byte
		geo = putVarint(geo, 9) // MoveTo, count 1
		geo = putVarint(geo, zigzag(pt[0]))
		geo = putVarint(geo, zigzag(pt[1]))
		f = putBytesField(f, 4, geo)

		feats = append(feats, f)
	}

	// Field numbers as parsed by mvtgo: name=1, features=2, keys=3,
	// values=4, extent=5.
	var layer []byte
	layer = putString(layer, 1, layerName) // name
	for _, f := range feats {
		layer = putBytesField(layer, 2, f) // features
	}
	layer = putString(layer, 3, key)
	layer = putBytesField(layer, 4, valueMsg)
	layer = putField(layer, 5, 0)
	layer = putVarint(layer, uint64(extent)) // extent

	var tile []byte
	tile = putBytesField(tile, 3, layer) // layers
	return tile
}
