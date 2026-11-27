package analysis

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"insightflow/api/internal/platform/dbgen"
)

func column(name, original, typ string, nullable bool) dbgen.DatasetColumn {
	return dbgen.DatasetColumn{
		ID:           pgtype.UUID{Bytes: uuid.New(), Valid: true},
		ColumnName:   name,
		OriginalName: original,
		InferredType: typ,
		IsNullable:   nullable,
	}
}

func message(role, content string) dbgen.AnalysisMessage {
	return dbgen.AnalysisMessage{
		ID:      pgtype.UUID{Bytes: uuid.New(), Valid: true},
		Role:    role,
		Content: content,
	}
}

func TestBuildContextMergesColumnMetadataWithProfile(t *testing.T) {
	profile := dbgen.DatasetProfile{
		ProfileJson: []byte(`{
			"quality_score": 91.5,
			"columns": [
				{"name": "amount", "null_pct": 2.5, "distinct_count": 40,
				 "stats": {"min": 10, "max": 990, "mean": 300.5},
				 "top_values": [{"value": "10", "count": 3}]},
				{"name": "unknown_column", "null_pct": 0}
			]
		}`),
	}

	got, err := BuildContext("ds-1", 2, 500, 2,
		[]dbgen.DatasetColumn{column("amount", "Amount", "number", false), column("category", "Category", "text", true)},
		&profile, nil)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}

	if got.Dataset.DatasetID != "ds-1" || got.Dataset.VersionNo != 2 || got.Dataset.RowCount != 500 {
		t.Fatalf("dataset header = %+v", got.Dataset)
	}
	if got.Dataset.QualityScore == nil || *got.Dataset.QualityScore != 91.5 {
		t.Fatalf("quality score = %v, want 91.5", got.Dataset.QualityScore)
	}
	if len(got.Dataset.Columns) != 2 {
		t.Fatalf("columns = %d, want 2 (profile-only columns must not appear)", len(got.Dataset.Columns))
	}
	amount := got.Dataset.Columns[0]
	if amount.Name != "amount" || amount.OriginalName != "Amount" || amount.Type != "number" {
		t.Fatalf("column identity = %+v", amount)
	}
	if amount.NullPct == nil || *amount.NullPct != 2.5 {
		t.Fatalf("null pct = %v, want 2.5", amount.NullPct)
	}
	if amount.Distinct == nil || *amount.Distinct != 40 {
		t.Fatalf("distinct = %v, want 40", amount.Distinct)
	}
	if amount.Stats["mean"] != 300.5 {
		t.Fatalf("stats = %+v", amount.Stats)
	}
	if len(amount.TopValues) != 1 || amount.TopValues[0].Value != "10" || amount.TopValues[0].Count != 3 {
		t.Fatalf("top values = %+v", amount.TopValues)
	}
	// A column with no profile entry keeps its metadata and omits statistics.
	if got.Dataset.Columns[1].NullPct != nil || got.Dataset.Columns[1].Stats != nil {
		t.Fatalf("unprofiled column leaked stats: %+v", got.Dataset.Columns[1])
	}
}

func TestBuildContextWithoutProfile(t *testing.T) {
	got, err := BuildContext("ds-1", 1, 10, 1, []dbgen.DatasetColumn{column("amount", "Amount", "number", true)}, nil, nil)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if got.Dataset.QualityScore != nil {
		t.Fatalf("quality score = %v, want nil", got.Dataset.QualityScore)
	}
	if len(got.Dataset.Columns) != 1 || got.Dataset.Columns[0].Stats != nil {
		t.Fatalf("columns = %+v", got.Dataset.Columns)
	}
}

func TestBuildContextRejectsMalformedProfile(t *testing.T) {
	profile := dbgen.DatasetProfile{ProfileJson: []byte(`{not json`)}
	if _, err := BuildContext("ds-1", 1, 1, 1, nil, &profile, nil); err == nil {
		t.Fatal("expected an error for malformed profile json")
	}
}

func TestBuildContextTrimsHistoryToNewestTurns(t *testing.T) {
	history := []dbgen.AnalysisMessage{
		message("user", "q1"), message("assistant", "a1"),
		message("user", "q2"), message("assistant", "a2"),
		message("user", "q3"), message("assistant", "a3"),
		message("user", "q4"),
	}
	got, err := BuildContext("ds-1", 1, 1, 1, nil, nil, history)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if len(got.Messages) != contextMessageLimit {
		t.Fatalf("messages = %d, want %d", len(got.Messages), contextMessageLimit)
	}
	if got.Messages[0].Content != "q2" || got.Messages[len(got.Messages)-1].Content != "q4" {
		t.Fatalf("kept messages = %+v, want the newest %d oldest-first", got.Messages, contextMessageLimit)
	}
}

func TestBuildContextKeepsShortHistoryUnchanged(t *testing.T) {
	history := []dbgen.AnalysisMessage{message("user", "only")}
	got, err := BuildContext("ds-1", 1, 1, 1, nil, nil, history)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "only" {
		t.Fatalf("messages = %+v", got.Messages)
	}
}

func TestContextPayloadNeverCarriesRowValues(t *testing.T) {
	// The engine contract forbids raw rows: only schema, aggregate stats, and a
	// bounded top-values list may appear (Plan 06 §6 SEC-LLM-DATA).
	profile := dbgen.DatasetProfile{ProfileJson: []byte(`{"columns": [
		{"name": "email", "null_pct": 0, "distinct_count": 2,
		 "top_values": [{"value": "a@example.com", "count": 1}]}
	]}`)}
	got, err := BuildContext("ds-1", 1, 2, 1,
		[]dbgen.DatasetColumn{column("email", "Email", "text", false)}, &profile, nil)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Top values are an aggregate summary and are allowed, but no key named
	// "rows" or "sample" may exist.
	if strings.Contains(string(raw), `"rows"`) || strings.Contains(string(raw), `"sample"`) {
		t.Fatalf("context leaked row data: %s", raw)
	}
}
