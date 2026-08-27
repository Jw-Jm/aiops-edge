package graph

import (
	"context"
	"fmt"
)

var BackfillOrder = []string{"catalog", "hardware", "kubernetes", "kubevirt", "middleware", "trace", "change", "network"}

type BackfillSource interface {
	Backfill(context.Context) error
}

type BackfillReport struct {
	Completed []string `json:"completed"`
	Failed    string   `json:"failed,omitempty"`
}

// RunBackfill is intentionally serial: it preserves source ordering and makes
// a partial report safe to resume without overlapping mutation ownership.
func RunBackfill(ctx context.Context, sources map[string]BackfillSource) (BackfillReport, error) {
	report := BackfillReport{Completed: []string{}}
	for _, source := range BackfillOrder {
		item, ok := sources[source]
		if !ok {
			continue
		}
		if err := item.Backfill(ctx); err != nil {
			report.Failed = source
			return report, fmt.Errorf("backfill %s: %w", source, err)
		}
		report.Completed = append(report.Completed, source)
	}
	return report, nil
}
