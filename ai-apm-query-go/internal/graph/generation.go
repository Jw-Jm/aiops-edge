package graph

import "time"

var staleGrace = map[string]time.Duration{
	"kubernetes": 15 * time.Minute,
	"kubevirt":   5 * time.Minute,
	"hardware":   24 * time.Hour,
	"trace":      30 * time.Minute,
	"middleware": 30 * time.Minute,
	"network":    time.Hour,
}

// StaleGrace returns the source-specific grace before a missing observation may
// be deleted. Catalog objects are authoritative deletes and have no grace.
func StaleGrace(source string) time.Duration { return staleGrace[source] }

func NextGeneration(previous int64) int64 {
	if previous < 0 {
		return 1
	}
	return previous + 1
}

// StaleEligible is deliberately conservative: zero timestamps are never
// treated as old data, preventing incomplete source snapshots from deleting a
// healthy projection.
func StaleEligible(source string, lastSeen, now time.Time) bool {
	grace := StaleGrace(source)
	if grace <= 0 || lastSeen.IsZero() || now.Before(lastSeen) {
		return false
	}
	return now.Sub(lastSeen) >= grace
}
