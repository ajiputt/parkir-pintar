package pricing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTimeRange_Overlaps(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 5, 1, h, 0, 0, 0, time.UTC) }

	cases := []struct {
		name string
		a, b TimeRange
		want bool
	}{
		{"disjoint earlier", TimeRange{at(8), at(10)}, TimeRange{at(11), at(12)}, false},
		{"disjoint later", TimeRange{at(15), at(16)}, TimeRange{at(8), at(10)}, false},
		{"touching boundary (a.end == b.start)", TimeRange{at(8), at(10)}, TimeRange{at(10), at(12)}, false},
		{"partial left overlap", TimeRange{at(8), at(11)}, TimeRange{at(10), at(12)}, true},
		{"partial right overlap", TimeRange{at(10), at(13)}, TimeRange{at(8), at(11)}, true},
		{"a contains b", TimeRange{at(8), at(20)}, TimeRange{at(10), at(12)}, true},
		{"b contains a", TimeRange{at(10), at(11)}, TimeRange{at(8), at(20)}, true},
		{"identical", TimeRange{at(8), at(10)}, TimeRange{at(8), at(10)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, c.a.Overlaps(c.b))
			assert.Equal(t, c.want, c.b.Overlaps(c.a), "must be commutative")
		})
	}
}

func TestTimeRange_Contains(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 5, 1, h, 0, 0, 0, time.UTC) }
	r := TimeRange{at(8), at(10)}
	assert.True(t, r.Contains(at(8)))   // start inclusive
	assert.True(t, r.Contains(at(9)))   // mid
	assert.False(t, r.Contains(at(10))) // end exclusive
	assert.False(t, r.Contains(at(7)))  // before
	assert.False(t, r.Contains(at(11))) // after
}
