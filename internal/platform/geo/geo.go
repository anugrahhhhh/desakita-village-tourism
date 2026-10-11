// Package geo holds the coordinate rules shared by village locations and the map tiles.
package geo

import "math"

// Bounds is a latitude/longitude rectangle.
type Bounds struct{ South, West, North, East float64 }

// Indonesia covers the archipelago with a small margin. Village coordinates must fall inside
// it, and the tile server only serves detailed tiles that intersect it.
var Indonesia = Bounds{South: -11.5, West: 94.0, North: 6.5, East: 141.5}

func (b Bounds) Contains(lat, lng float64) bool {
	return lat >= b.South && lat <= b.North && lng >= b.West && lng <= b.East
}

// TileRange returns the inclusive x and y tile indices that cover b at zoom z (Web Mercator,
// the scheme used by OpenStreetMap and Leaflet).
func (b Bounds) TileRange(z int) (minX, minY, maxX, maxY int) {
	minX, minY = tileXY(b.North, b.West, z)
	maxX, maxY = tileXY(b.South, b.East, z)
	return
}

func tileXY(lat, lng float64, z int) (x, y int) {
	n := math.Exp2(float64(z))
	r := lat * math.Pi / 180
	x = int(math.Floor((lng + 180) / 360 * n))
	y = int(math.Floor((1 - math.Log(math.Tan(r)+1/math.Cos(r))/math.Pi) / 2 * n))
	clamp := func(v int) int { return max(0, min(v, int(n)-1)) }
	return clamp(x), clamp(y)
}
