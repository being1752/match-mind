package database

import (
	"encoding/json"
	"testing"

	"matchmind/internal/models"
)

func TestIsAggregateItem(t *testing.T) {
	tests := []struct {
		name string
		item models.StructuredItem
		want bool
	}{
		{"generic title", models.StructuredItem{Title: "多个项目寻求并购", StructuredData: json.RawMessage(`{}`)}, true},
		{"nested demands", models.StructuredItem{Title: "收购需求", StructuredData: json.RawMessage(`{"extra_constraints":{"sub_requirements":[{},{}]}}`)}, true},
		{"atomic entity", models.StructuredItem{Title: "智能制造项目寻求并购", StructuredData: json.RawMessage(`{"industries":["智能制造"]}`)}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isAggregateItem(test.item); got != test.want {
				t.Fatalf("isAggregateItem()=%v, want %v", got, test.want)
			}
		})
	}
}
