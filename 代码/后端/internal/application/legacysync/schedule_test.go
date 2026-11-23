package legacysync

import (
	"testing"
	"time"
)

func TestLatestScheduledNoonInBeijing(t *testing.T) {
	before := time.Date(2026, 11, 23, 3, 59, 59, 0, time.UTC)
	for _, test := range []struct {
		now time.Time
		day int
	}{{before, 22}, {before.Add(time.Second), 23}, {before.Add(8 * time.Hour), 23}} {
		got := ScheduledNoon(test.now).UTC()
		if got.Day() != test.day || got.Hour() != 4 || got.Minute() != 0 || got.Second() != 0 {
			t.Errorf("got %s for %s", got, test.now)
		}
	}
}
