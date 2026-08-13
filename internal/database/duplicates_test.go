package database

import "testing"

func TestCoreFieldsContainmentAllowsSupplement(t *testing.T) {
	base := map[string]any{
		"transaction_types":         []any{"收购控股权"},
		"target_types":              []any{"上市公司"},
		"target_valuation_min":      float64(3000000000),
		"target_valuation_max":      float64(4000000000),
		"listed_status_requirement": "A股上市公司",
		"control_ratio_min":         float64(0.4),
	}
	supplement := map[string]any{
		"transaction_types":         []any{"收购控股权"},
		"target_types":              []any{"上市公司"},
		"target_valuation_min":      float64(3000000000),
		"target_valuation_max":      float64(4000000000),
		"listed_status_requirement": "A股上市公司",
		"control_ratio_min":         float64(0.4),
		"extra_constraints":         map[string]any{"优先考虑": "新三板公司"},
	}
	if !coreFieldsContainment("buy_demand", base, supplement) {
		t.Fatal("expected the second demand to be recognized as supplementary information")
	}
}

func TestCoreFieldsContainmentRejectsCoreConflict(t *testing.T) {
	left := map[string]any{
		"transaction_types": []any{"收购控股权"}, "target_types": []any{"上市公司"},
		"target_valuation_min": float64(3000000000), "target_valuation_max": float64(4000000000),
		"listed_status_requirement": "A股上市公司", "control_ratio_min": float64(0.4),
	}
	right := map[string]any{
		"transaction_types": []any{"收购控股权"}, "target_types": []any{"上市公司"},
		"target_valuation_min": float64(3000000000), "target_valuation_max": float64(4000000000),
		"listed_status_requirement": "A股上市公司", "control_ratio_min": float64(0.6),
	}
	if coreFieldsContainment("buy_demand", left, right) {
		t.Fatal("core field conflicts must not auto-merge")
	}
}

func TestCoreFieldsContainmentRejectsSeparatelyNumberedSeries(t *testing.T) {
	left := map[string]any{
		"title": "医药上市公司控转（一）", "industries": []any{"医药"},
		"transaction_types": []any{"控股权转让"}, "project_types": []any{"上市公司"},
		"net_profit_min": float64(100000000), "listed_status": "上市",
	}
	right := map[string]any{
		"title": "医药上市公司控转（二）", "industries": []any{"医药"},
		"transaction_types": []any{"控股权转让"}, "project_types": []any{"上市公司"},
		"net_profit_min": float64(100000000), "listed_status": "上市",
	}
	if coreFieldsContainment("sell_project", left, right) {
		t.Fatal("separately numbered projects must not auto-merge")
	}
}
