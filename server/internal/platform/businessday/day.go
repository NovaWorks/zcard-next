// Package businessday defines the shop reporting calendar independently of host timezone.
package businessday

import "time"

// Location is Beijing time. Database timestamps remain absolute UTC instants.
var Location = time.FixedZone("Asia/Shanghai", 8*60*60)

func Now() time.Time { return time.Now().In(Location) }
func Start(t time.Time) time.Time {
	t = t.In(Location)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, Location).UTC()
}
func Date(t time.Time, layout string) string { return t.In(Location).Format(layout) }
