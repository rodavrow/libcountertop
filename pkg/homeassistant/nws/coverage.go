package nws

// The National Weather Service issues alerts for the United States and its territories only.
// These boxes are generous: a point just across a border gets an empty answer, which is harmless;
// a point far outside them is never asked about.
var coverage = []struct{ south, north, west, east float64 }{
	{24.0, 50.0, -125.5, -66.5},    // the contiguous states
	{51.0, 72.0, -180.0, -129.0},   // Alaska
	{51.0, 54.0, 172.0, 180.0},     // the western Aleutians
	{18.5, 22.5, -161.0, -154.5},   // Hawaii
	{17.5, 18.8, -68.0, -64.3},     // Puerto Rico and the Virgin Islands
	{13.0, 21.0, 144.0, 146.5},     // Guam and the Northern Marianas
	{-14.8, -10.8, -171.5, -168.0}, // American Samoa
}

func covered(lat, lon float64) bool {
	for _, b := range coverage {
		if lat >= b.south && lat <= b.north && lon >= b.west && lon <= b.east {
			return true
		}
	}
	return false
}
