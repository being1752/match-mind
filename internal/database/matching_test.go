package database

import "testing"

func TestStructuredMatchDetails(t *testing.T) {
	buy := map[string]any{
		"industries":            []any{"IDC", "算力中心"},
		"transaction_types":     []any{"acquisition"},
		"preferred_regions":     []any{"上海"},
		"target_net_profit_min": float64(30000000),
	}
	sell := map[string]any{
		"industries":        []any{"IDC"},
		"transaction_types": []any{"acquisition"},
		"net_profit_min":    float64(35000000),
		"net_profit_max":    float64(35000000),
	}
	score, assessment := structuredMatchDetails(buy, sell)
	if score < 0.99 {
		t.Fatalf("unexpected score %f", score)
	}
	if len(assessment.Matched) != 3 {
		t.Fatalf("matched=%d, want 3", len(assessment.Matched))
	}
	if len(assessment.Missing) != 1 {
		t.Fatalf("missing=%d, want 1 visible one-sided field", len(assessment.Missing))
	}
	if assessment.Completeness != 0.5 {
		t.Fatalf("completeness=%f, want .5", assessment.Completeness)
	}
}

func TestStructuredMatchDetailsOmitsFieldsMissingOnBothSides(t *testing.T) {
	buy := map[string]any{"industries": []any{"人工智能"}}
	sell := map[string]any{"industries": []any{"人工智能"}}
	_, assessment := structuredMatchDetails(buy, sell)
	if len(assessment.Matched) != 1 {
		t.Fatalf("matched=%d, want 1", len(assessment.Matched))
	}
	if len(assessment.Missing) != 0 {
		t.Fatalf("missing=%d, fields undisclosed by both sides must be hidden", len(assessment.Missing))
	}
}

func TestStructuredMatchDetailsReportsConflicts(t *testing.T) {
	buy := map[string]any{
		"industries":        []any{"医疗"},
		"preferred_regions": []any{"上海"},
		"control_ratio_min": float64(0.51),
	}
	sell := map[string]any{
		"industries":         []any{"矿产"},
		"province":           "北京",
		"transfer_ratio_max": float64(0.30),
	}
	_, assessment := structuredMatchDetails(buy, sell)
	if len(assessment.Conflicts) != 3 {
		t.Fatalf("conflicts=%d, want 3", len(assessment.Conflicts))
	}
}

func TestMatchLevel(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{{.82, "值得跟进"}, {.7, "建议了解"}, {.5, "可能相关"}}
	for _, test := range tests {
		_, got := matchLevel(test.score)
		if got != test.want {
			t.Fatalf("score %.2f level=%s, want %s", test.score, got, test.want)
		}
	}
}
