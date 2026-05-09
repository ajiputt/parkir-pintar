package pricing

import "time"

// TimeRange — closed-open [start, end). Helper untuk overlap detection
// di unit test (sisi DB sudah dijamin oleh EXCLUDE constraint, ini untuk
// validasi business rule pre-DB).
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// Overlaps — true kalau dua range punya intersection non-zero.
func (r TimeRange) Overlaps(other TimeRange) bool {
	return r.Start.Before(other.End) && other.Start.Before(r.End)
}

// Contains — true kalau t berada dalam [Start, End).
func (r TimeRange) Contains(t time.Time) bool {
	return !t.Before(r.Start) && t.Before(r.End)
}
