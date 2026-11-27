package analysis

import (
	"encoding/json"
	"fmt"

	"insightflow/api/internal/platform/dbgen"
)

// contextMessageLimit caps the conversation history sent to the engine so the
// prompt stays inside the model's context window (Plan 06 §2).
const contextMessageLimit = 5

// ColumnContext is one column of the dataset schema. Only metadata and
// aggregate statistics travel to the engine: never row values.
type ColumnContext struct {
	Name         string         `json:"name"`
	OriginalName string         `json:"original_name"`
	Type         string         `json:"type"`
	Nullable     bool           `json:"nullable"`
	NullPct      *float64       `json:"null_pct,omitempty"`
	Distinct     *int64         `json:"distinct_count,omitempty"`
	Stats        map[string]any `json:"stats,omitempty"`
	TopValues    []topValue     `json:"top_values,omitempty"`
}

type topValue struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// DatasetContext is the schema and profile summary for one dataset version.
type DatasetContext struct {
	DatasetID    string          `json:"dataset_id"`
	VersionNo    int32           `json:"version_no"`
	RowCount     int64           `json:"row_count"`
	ColumnCount  int32           `json:"column_count"`
	QualityScore *float64        `json:"quality_score,omitempty"`
	Columns      []ColumnContext `json:"columns"`
}

// ContextMessage is one prior turn in the conversation.
type ContextMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AnalysisContext is the full payload the engine planner receives.
type AnalysisContext struct {
	Dataset  DatasetContext   `json:"dataset"`
	Messages []ContextMessage `json:"messages"`
}

// BuildContext assembles the engine payload from persisted metadata. columns and
// profile are the version's rows; profile may be nil when profiling was skipped.
// history is trimmed to the newest contextMessageLimit turns.
func BuildContext(datasetID string, versionNo int32, rowCount int64, columnCount int32, columns []dbgen.DatasetColumn, profile *dbgen.DatasetProfile, history []dbgen.AnalysisMessage) (AnalysisContext, error) {
	stats, quality, err := parseProfile(profile)
	if err != nil {
		return AnalysisContext{}, err
	}

	out := AnalysisContext{
		Dataset: DatasetContext{
			DatasetID:    datasetID,
			VersionNo:    versionNo,
			RowCount:     rowCount,
			ColumnCount:  columnCount,
			QualityScore: quality,
			Columns:      make([]ColumnContext, 0, len(columns)),
		},
		Messages: trimHistory(history),
	}
	for _, column := range columns {
		cc := ColumnContext{
			Name:         column.ColumnName,
			OriginalName: column.OriginalName,
			Type:         column.InferredType,
			Nullable:     column.IsNullable,
		}
		if s, ok := stats[column.ColumnName]; ok {
			cc.NullPct = s.nullPct
			cc.Distinct = s.distinct
			cc.Stats = s.stats
			cc.TopValues = s.topValues
		}
		out.Dataset.Columns = append(out.Dataset.Columns, cc)
	}
	return out, nil
}

type columnStats struct {
	nullPct   *float64
	distinct  *int64
	stats     map[string]any
	topValues []topValue
}

// profileColumns mirrors the subset of the profiler's jsonb output the context
// needs. Unknown fields are ignored so a newer profile shape cannot break this.
type profileJSON struct {
	QualityScore *float64 `json:"quality_score"`
	Columns      []struct {
		Name      string         `json:"name"`
		NullPct   *float64       `json:"null_pct"`
		Distinct  *int64         `json:"distinct_count"`
		Stats     map[string]any `json:"stats"`
		TopValues []struct {
			Value string `json:"value"`
			Count int64  `json:"count"`
		} `json:"top_values"`
	} `json:"columns"`
}

func parseProfile(profile *dbgen.DatasetProfile) (map[string]columnStats, *float64, error) {
	if profile == nil || len(profile.ProfileJson) == 0 {
		return nil, nil, nil
	}
	var parsed profileJSON
	if err := json.Unmarshal(profile.ProfileJson, &parsed); err != nil {
		return nil, nil, fmt.Errorf("analysis: parse profile: %w", err)
	}
	stats := make(map[string]columnStats, len(parsed.Columns))
	for _, c := range parsed.Columns {
		top := make([]topValue, 0, len(c.TopValues))
		for _, t := range c.TopValues {
			top = append(top, topValue{Value: t.Value, Count: t.Count})
		}
		stats[c.Name] = columnStats{
			nullPct:   c.NullPct,
			distinct:  c.Distinct,
			stats:     c.Stats,
			topValues: top,
		}
	}
	return stats, parsed.QualityScore, nil
}

// trimHistory keeps the newest contextMessageLimit messages, oldest-first.
func trimHistory(history []dbgen.AnalysisMessage) []ContextMessage {
	start := 0
	if len(history) > contextMessageLimit {
		start = len(history) - contextMessageLimit
	}
	out := make([]ContextMessage, 0, len(history)-start)
	for _, m := range history[start:] {
		out = append(out, ContextMessage{Role: m.Role, Content: m.Content})
	}
	return out
}
