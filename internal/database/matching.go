package database

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

type MatchSignal struct {
	Field       string `json:"field"`
	Label       string `json:"label"`
	BuyerValue  string `json:"buyer_value,omitempty"`
	SellerValue string `json:"seller_value,omitempty"`
	Message     string `json:"message"`
}

type MatchAssessment struct {
	Matched      []MatchSignal `json:"matched"`
	Conflicts    []MatchSignal `json:"conflicts"`
	Missing      []MatchSignal `json:"missing"`
	Completeness float64       `json:"completeness"`
}

type MatchResult struct {
	EntityType      string          `json:"entity_type"`
	EntityID        int64           `json:"entity_id"`
	Title           string          `json:"title"`
	StructuredScore float64         `json:"structured_score"`
	VectorScore     float64         `json:"vector_score"`
	FinalScore      float64         `json:"final_score"`
	Reasons         json.RawMessage `json:"reasons"`
	Level           string          `json:"level"`
	LevelText       string          `json:"level_text"`
	Completeness    float64         `json:"completeness"`
	Matched         []MatchSignal   `json:"matched"`
	Conflicts       []MatchSignal   `json:"conflicts"`
	Missing         []MatchSignal   `json:"missing"`
}

func (s *Store) PostProcessEmbedding(ctx context.Context, entityType string, entityID int64, model string) error {
	if err := s.EvaluateDuplicate(ctx, entityType, entityID, model); err != nil {
		return err
	}
	return s.RebuildMatches(ctx, entityType, entityID, model)
}

func (s *Store) RebuildAllMatches(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.entity_id,e.model_name
		FROM entity_embeddings e JOIN buy_demands b ON b.id=e.entity_id
		WHERE e.entity_type='buy_demand' AND e.embedding_type='matching_v1'
			AND e.status='completed' AND e.embedding IS NOT NULL AND b.status='active'
		ORDER BY e.entity_id`)
	if err != nil {
		return 0, err
	}
	type source struct {
		id    int64
		model string
	}
	sources := []source{}
	for rows.Next() {
		var item source
		if err := rows.Scan(&item.id, &item.model); err != nil {
			rows.Close()
			return 0, err
		}
		sources = append(sources, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, item := range sources {
		if err := s.RebuildMatches(ctx, "buy_demand", item.id, item.model); err != nil {
			return 0, fmt.Errorf("rebuild matches for buy_demand %d: %w", item.id, err)
		}
	}
	return len(sources), nil
}

func (s *Store) RebuildMatches(ctx context.Context, entityType string, entityID int64, model string) error {
	sourceTable, err := entityTable(entityType)
	if err != nil {
		return err
	}
	oppositeType := "sell_project"
	oppositeTable := "sell_projects"
	if entityType == "sell_project" {
		oppositeType = "buy_demand"
		oppositeTable = "buy_demands"
	}
	var sourceJSON []byte
	if err := s.pool.QueryRow(ctx, fmt.Sprintf(`SELECT to_jsonb(t) FROM %s t WHERE id=$1 AND status='active'`, sourceTable), entityID).Scan(&sourceJSON); err != nil {
		return err
	}
	var source map[string]any
	if err := json.Unmarshal(sourceJSON, &source); err != nil {
		return err
	}
	if entityType == "buy_demand" {
		if _, err := s.pool.Exec(ctx, `DELETE FROM match_results WHERE buy_demand_id=$1`, entityID); err != nil {
			return err
		}
	} else if _, err := s.pool.Exec(ctx, `DELETE FROM match_results WHERE sell_project_id=$1`, entityID); err != nil {
		return err
	}
	query := fmt.Sprintf(`
		WITH source AS (
			SELECT embedding FROM entity_embeddings
			WHERE entity_type=$1 AND entity_id=$2 AND embedding_type='matching_v1' AND model_name=$3
		)
		SELECT e.entity_id,1-(e.embedding <=> source.embedding),to_jsonb(t)
		FROM entity_embeddings e CROSS JOIN source JOIN %s t ON t.id=e.entity_id
		WHERE e.entity_type=$4 AND e.embedding_type='matching_v1' AND e.model_name=$3
			AND e.status='completed' AND t.status='active'
		ORDER BY e.embedding <=> source.embedding LIMIT 20`, oppositeTable)
	rows, err := s.pool.Query(ctx, query, entityType, entityID, model, oppositeType)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var candidateID int64
		var vectorScore float64
		var candidateJSON []byte
		if err := rows.Scan(&candidateID, &vectorScore, &candidateJSON); err != nil {
			return err
		}
		var candidate map[string]any
		if err := json.Unmarshal(candidateJSON, &candidate); err != nil {
			return err
		}
		buy, sell := source, candidate
		buyID, sellID := entityID, candidateID
		if entityType == "sell_project" {
			buy, sell = candidate, source
			buyID, sellID = candidateID, entityID
		}
		structuredScore, _ := structuredMatchDetails(buy, sell)
		finalScore := clamp(vectorScore*0.75 + structuredScore*0.25)
		if finalScore < 0.5 {
			continue
		}
		// Explanations are computed from the latest entity fields when read.
		reasonsJSON := []byte(`{}`)
		_, err = s.pool.Exec(ctx, `
			INSERT INTO match_results(buy_demand_id,sell_project_id,structured_score,vector_score,final_score,reasons)
			VALUES($1,$2,$3,$4,$5,$6)
			ON CONFLICT(buy_demand_id,sell_project_id,scoring_version)
			DO UPDATE SET structured_score=EXCLUDED.structured_score,vector_score=EXCLUDED.vector_score,
				final_score=EXCLUDED.final_score,reasons=EXCLUDED.reasons,updated_at=NOW()`,
			buyID, sellID, structuredScore, vectorScore, finalScore, reasonsJSON)
		if err != nil {
			return err
		}
	}
	return rows.Err()
}

func structuredMatch(buy, sell map[string]any) (float64, []string) {
	score, assessment := structuredMatchDetails(buy, sell)
	reasons := make([]string, 0, len(assessment.Matched))
	for _, signal := range assessment.Matched {
		reasons = append(reasons, signal.Message)
	}
	return score, reasons
}

func structuredMatchDetails(buy, sell map[string]any) (float64, MatchAssessment) {
	score := 0.0
	weight := 0.0
	known := 0
	total := 6
	assessment := MatchAssessment{Matched: []MatchSignal{}, Conflicts: []MatchSignal{}, Missing: []MatchSignal{}}
	add := func(status string, signal MatchSignal) {
		// A comparison row is useful when at least one side disclosed a value.
		// If neither side disclosed it, omit it from the response entirely.
		if strings.TrimSpace(signal.BuyerValue) == "" && strings.TrimSpace(signal.SellerValue) == "" {
			return
		}
		switch status {
		case "matched":
			assessment.Matched = append(assessment.Matched, signal)
		case "conflict":
			assessment.Conflicts = append(assessment.Conflicts, signal)
		default:
			assessment.Missing = append(assessment.Missing, signal)
		}
	}
	if available, matched := arrayMatch(buy["industries"], sell["industries"]); available {
		known++
		weight += 0.5
		if matched {
			score += 0.5
			add("matched", comparisonSignal("industry", "行业", joinValues(buy["industries"]), joinValues(sell["industries"]), "行业方向符合"))
		} else {
			add("conflict", comparisonSignal("industry", "行业", joinValues(buy["industries"]), joinValues(sell["industries"]), "行业方向不一致"))
		}
	} else {
		add("missing", missingSignal("industry", "行业", buy["industries"], sell["industries"]))
	}
	if available, matched := arrayMatch(buy["transaction_types"], sell["transaction_types"]); available {
		known++
		weight += 0.2
		if matched {
			score += 0.2
			add("matched", comparisonSignal("transaction_type", "交易方式", joinValues(buy["transaction_types"]), joinValues(sell["transaction_types"]), "交易方式符合"))
		} else {
			add("conflict", comparisonSignal("transaction_type", "交易方式", joinValues(buy["transaction_types"]), joinValues(sell["transaction_types"]), "交易方式不一致"))
		}
	} else {
		add("missing", missingSignal("transaction_type", "交易方式", buy["transaction_types"], sell["transaction_types"]))
	}
	regions := stringArray(buy["preferred_regions"])
	location := stringsFromValues(sell["province"], sell["city"])
	if len(regions) > 0 && len(location) > 0 {
		known++
		weight += 0.15
		if anyContains(regions, location) {
			score += 0.15
			add("matched", comparisonSignal("region", "地区", strings.Join(regions, "、"), strings.Join(location, "、"), "地区符合偏好"))
		} else {
			add("conflict", comparisonSignal("region", "地区", strings.Join(regions, "、"), strings.Join(location, "、"), "地区不符合偏好"))
		}
	} else {
		add("missing", missingSignal("region", "地区", regions, location))
	}
	if available, matched := amountCompatible(buy, sell); available {
		known++
		weight += 0.15
		if matched {
			score += 0.15
			add("matched", comparisonSignal("amount", "交易金额", buyAmountText(buy), sellAmountText(sell), "金额区间相容"))
		} else {
			add("conflict", comparisonSignal("amount", "交易金额", buyAmountText(buy), sellAmountText(sell), "金额区间不相容"))
		}
	} else {
		add("missing", MatchSignal{Field: "amount", Label: "交易金额", BuyerValue: buyAmountText(buy), SellerValue: sellAmountText(sell), Message: missingMessage("交易金额", buyAmountText(buy), sellAmountText(sell))})
	}
	if available, matched := profitCompatible(buy, sell); available {
		known++
		if matched {
			add("matched", comparisonSignal("profit", "净利润", buyProfitText(buy), sellProfitText(sell), "净利润符合要求"))
		} else {
			add("conflict", comparisonSignal("profit", "净利润", buyProfitText(buy), sellProfitText(sell), "净利润不符合要求"))
		}
	} else {
		add("missing", MatchSignal{Field: "profit", Label: "净利润", BuyerValue: buyProfitText(buy), SellerValue: sellProfitText(sell), Message: missingMessage("净利润", buyProfitText(buy), sellProfitText(sell))})
	}
	if available, matched := controlCompatible(buy, sell); available {
		known++
		if matched {
			add("matched", comparisonSignal("control_ratio", "股权比例", percentText(buy["control_ratio_min"]), sellRatioText(sell), "股权比例符合要求"))
		} else {
			add("conflict", comparisonSignal("control_ratio", "股权比例", percentText(buy["control_ratio_min"]), sellRatioText(sell), "股权比例不符合要求"))
		}
	} else {
		add("missing", MatchSignal{Field: "control_ratio", Label: "股权比例", BuyerValue: percentText(buy["control_ratio_min"]), SellerValue: sellRatioText(sell), Message: missingMessage("股权比例", percentText(buy["control_ratio_min"]), sellRatioText(sell))})
	}
	assessment.Completeness = float64(known) / float64(total)
	if weight == 0 {
		return 0, assessment
	}
	return clamp(score / weight), assessment
}

func comparisonSignal(field, label, buyer, seller, message string) MatchSignal {
	return MatchSignal{Field: field, Label: label, BuyerValue: buyer, SellerValue: seller, Message: message}
}

func missingSignal(field, label string, buyer, seller any) MatchSignal {
	buyerText, sellerText := joinValues(buyer), joinValues(seller)
	return MatchSignal{Field: field, Label: label, BuyerValue: buyerText, SellerValue: sellerText, Message: missingMessage(label, buyerText, sellerText)}
}

func missingMessage(label, buyer, seller string) string {
	if buyer == "" && seller == "" {
		return "买卖双方均未披露" + label
	}
	if buyer == "" {
		return "买方未披露" + label + "要求"
	}
	return "卖方未披露" + label
}

func joinValues(value any) string {
	values := stringArray(value)
	if typed, ok := value.([]string); ok {
		values = append([]string{}, typed...)
	}
	if len(values) == 0 {
		return ""
	}
	sort.Strings(values)
	return strings.Join(values, "、")
}

func profitCompatible(buy, sell map[string]any) (bool, bool) {
	buyMin, buyMinOK := number(buy["target_net_profit_min"])
	buyMax, buyMaxOK := number(buy["target_net_profit_max"])
	sellMin, sellMinOK := number(sell["net_profit_min"])
	sellMax, sellMaxOK := number(sell["net_profit_max"])
	if (!buyMinOK && !buyMaxOK) || (!sellMinOK && !sellMaxOK) {
		return false, false
	}
	if !buyMinOK {
		buyMin = -math.MaxFloat64
	}
	if !buyMaxOK {
		buyMax = math.MaxFloat64
	}
	if !sellMinOK {
		sellMin = sellMax
	}
	if !sellMaxOK {
		sellMax = sellMin
	}
	return true, buyMin <= sellMax && sellMin <= buyMax
}

func controlCompatible(buy, sell map[string]any) (bool, bool) {
	required, requiredOK := number(buy["control_ratio_min"])
	sellMin, sellMinOK := number(sell["transfer_ratio_min"])
	sellMax, sellMaxOK := number(sell["transfer_ratio_max"])
	if !requiredOK || (!sellMinOK && !sellMaxOK) {
		return false, false
	}
	if !sellMaxOK {
		sellMax = sellMin
	}
	return true, sellMax >= required
}

func buyAmountText(value map[string]any) string {
	return rangeText(value["investment_amount_min"], value["investment_amount_max"])
}

func sellAmountText(value map[string]any) string {
	text := rangeText(value["financing_amount_min"], value["financing_amount_max"])
	if text == "" {
		text = rangeText(value["valuation_min"], value["valuation_max"])
	}
	return text
}

func buyProfitText(value map[string]any) string {
	return rangeText(value["target_net_profit_min"], value["target_net_profit_max"])
}

func sellProfitText(value map[string]any) string {
	return rangeText(value["net_profit_min"], value["net_profit_max"])
}

func sellRatioText(value map[string]any) string {
	return percentRangeText(value["transfer_ratio_min"], value["transfer_ratio_max"])
}

func rangeText(minValue, maxValue any) string {
	min, minOK := number(minValue)
	max, maxOK := number(maxValue)
	if !minOK && !maxOK {
		return ""
	}
	if minOK && maxOK && min == max {
		return moneyText(min)
	}
	if minOK && maxOK {
		return moneyText(min) + "～" + moneyText(max)
	}
	if minOK {
		return "≥" + moneyText(min)
	}
	return "≤" + moneyText(max)
}

func moneyText(value float64) string {
	if math.Abs(value) >= 1e8 {
		return fmt.Sprintf("%.2g亿元", value/1e8)
	}
	if math.Abs(value) >= 1e4 {
		return fmt.Sprintf("%.4g万元", value/1e4)
	}
	return fmt.Sprintf("%.4g元", value)
}

func percentText(value any) string {
	number, ok := number(value)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%.1f%%", number*100)
}

func percentRangeText(minValue, maxValue any) string {
	min, minOK := number(minValue)
	max, maxOK := number(maxValue)
	if !minOK && !maxOK {
		return ""
	}
	if minOK && maxOK && min == max {
		return fmt.Sprintf("%.1f%%", min*100)
	}
	if minOK && maxOK {
		return fmt.Sprintf("%.1f%%～%.1f%%", min*100, max*100)
	}
	if minOK {
		return fmt.Sprintf("≥%.1f%%", min*100)
	}
	return fmt.Sprintf("≤%.1f%%", max*100)
}

func arrayMatch(left, right any) (bool, bool) {
	a, b := stringArray(left), stringArray(right)
	if len(a) == 0 || len(b) == 0 {
		return false, false
	}
	return true, anyContains(a, b)
}

func stringArray(value any) []string {
	items, _ := value.([]any)
	result := []string{}
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			result = append(result, text)
		}
	}
	return result
}

func stringsFromValues(values ...any) []string {
	result := []string{}
	for _, value := range values {
		if text, ok := value.(string); ok && text != "" {
			result = append(result, text)
		}
	}
	return result
}

func anyContains(left, right []string) bool {
	for _, a := range left {
		for _, b := range right {
			if a == b || (len(a) > 1 && len(b) > 1 && (contains(a, b) || contains(b, a))) {
				return true
			}
		}
	}
	return false
}

func contains(value, part string) bool {
	for index := 0; index+len(part) <= len(value); index++ {
		if value[index:index+len(part)] == part {
			return true
		}
	}
	return false
}

func amountCompatible(buy, sell map[string]any) (bool, bool) {
	buyMin, buyMinOK := number(buy["investment_amount_min"])
	buyMax, buyMaxOK := number(buy["investment_amount_max"])
	sellMin, sellMinOK := number(sell["financing_amount_min"])
	sellMax, sellMaxOK := number(sell["financing_amount_max"])
	if !sellMinOK && !sellMaxOK {
		sellMin, sellMinOK = number(sell["valuation_min"])
		sellMax, sellMaxOK = number(sell["valuation_max"])
	}
	if (!buyMinOK && !buyMaxOK) || (!sellMinOK && !sellMaxOK) {
		return false, false
	}
	if !buyMinOK {
		buyMin = 0
	}
	if !sellMinOK {
		sellMin = 0
	}
	if !buyMaxOK {
		buyMax = math.MaxFloat64
	}
	if !sellMaxOK {
		sellMax = math.MaxFloat64
	}
	return true, buyMin <= sellMax && sellMin <= buyMax
}

func number(value any) (float64, bool) {
	result, ok := value.(float64)
	return result, ok
}

func clamp(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func (s *Store) ListMatches(ctx context.Context, entityType string, entityID int64) ([]MatchResult, error) {
	var query string
	if entityType == "buy_demand" {
		query = `SELECT 'sell_project',s.id,s.title,m.structured_score,m.vector_score,m.final_score,to_jsonb(b),to_jsonb(s)
			FROM match_results m JOIN buy_demands b ON b.id=m.buy_demand_id JOIN sell_projects s ON s.id=m.sell_project_id
			WHERE m.buy_demand_id=$1 AND b.status='active' AND s.status='active' ORDER BY m.final_score DESC LIMIT 20`
	} else {
		query = `SELECT 'buy_demand',b.id,b.title,m.structured_score,m.vector_score,m.final_score,to_jsonb(b),to_jsonb(s)
			FROM match_results m JOIN buy_demands b ON b.id=m.buy_demand_id JOIN sell_projects s ON s.id=m.sell_project_id
			WHERE m.sell_project_id=$1 AND b.status='active' AND s.status='active' ORDER BY m.final_score DESC LIMIT 20`
	}
	rows, err := s.pool.Query(ctx, query, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []MatchResult{}
	for rows.Next() {
		var item MatchResult
		var buyJSON, sellJSON []byte
		if err := rows.Scan(&item.EntityType, &item.EntityID, &item.Title, &item.StructuredScore,
			&item.VectorScore, &item.FinalScore, &buyJSON, &sellJSON); err != nil {
			return nil, err
		}
		if item.FinalScore < 0.5 {
			continue
		}
		item.Level, item.LevelText = matchLevel(item.FinalScore)
		var buy, sell map[string]any
		if err := json.Unmarshal(buyJSON, &buy); err != nil {
			return nil, fmt.Errorf("decode buy demand for live match comparison: %w", err)
		}
		if err := json.Unmarshal(sellJSON, &sell); err != nil {
			return nil, fmt.Errorf("decode sell project for live match comparison: %w", err)
		}
		_, liveAssessment := structuredMatchDetails(buy, sell)
		item.Reasons = json.RawMessage(`{}`)
		item.Completeness = liveAssessment.Completeness
		item.Matched = nonNilSignals(liveAssessment.Matched)
		item.Conflicts = nonNilSignals(liveAssessment.Conflicts)
		item.Missing = nonNilSignals(liveAssessment.Missing)
		result = append(result, item)
	}
	return result, rows.Err()
}

func matchLevel(score float64) (string, string) {
	switch {
	case score >= 0.8:
		return "worth_following", "值得跟进"
	case score >= 0.65:
		return "recommended", "建议了解"
	default:
		return "possibly_relevant", "可能相关"
	}
}

func nonNilSignals(value []MatchSignal) []MatchSignal {
	if value == nil {
		return []MatchSignal{}
	}
	return value
}
