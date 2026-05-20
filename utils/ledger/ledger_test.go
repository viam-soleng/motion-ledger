package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// helper: build a ledger with the given events for a single detector.
func ledgerWith(name string, events []MotionEvent) *Ledger {
	return &Ledger{
		Detectors: map[string]*DetectorLedger{
			name: {Events: events},
		},
		LastPrune: time.Now().UTC(),
	}
}

// ---- LoadOrCreate ----

func TestLoadOrCreate_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nope.json")

	l, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if l == nil {
		t.Fatal("LoadOrCreate returned nil ledger")
	}
	if l.Detectors == nil {
		t.Fatal("Detectors map is nil; expected initialized empty map")
	}
	if len(l.Detectors) != 0 {
		t.Fatalf("expected empty detectors, got %d", len(l.Detectors))
	}
	if l.LastPrune.IsZero() {
		t.Fatal("LastPrune is zero; expected current time")
	}
}

func TestLoadOrCreate_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")

	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	l, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if l == nil || l.Detectors == nil {
		t.Fatal("expected fresh ledger after corrupt-file recovery")
	}

	// The corrupt file should have been quarantined.
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("expected %s.corrupt to exist: %v", path, err)
	}
	// Quarantined bytes should match the original input.
	data, err := os.ReadFile(path + ".corrupt")
	if err != nil {
		t.Fatalf("read corrupt quarantine: %v", err)
	}
	if string(data) != "{not json" {
		t.Fatalf("quarantine bytes mismatch: %q", string(data))
	}
}

func TestLoadOrCreate_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")

	now := time.Now().UTC().Truncate(time.Second)
	original := &Ledger{
		Detectors: map[string]*DetectorLedger{
			"det-1": {Events: []MotionEvent{
				{Timestamp: now, Confidence: 0.9},
			}},
		},
		LastPrune: now,
	}

	if err := WriteAtomic(path, original); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	loaded, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if loaded.Detectors["det-1"] == nil {
		t.Fatal("det-1 missing after load")
	}
	if len(loaded.Detectors["det-1"].Events) != 1 {
		t.Fatalf("event count mismatch: %d", len(loaded.Detectors["det-1"].Events))
	}
	got := loaded.Detectors["det-1"].Events[0]
	if !got.Timestamp.Equal(now) {
		t.Fatalf("timestamp mismatch: got %v, want %v", got.Timestamp, now)
	}
	if got.Confidence != 0.9 {
		t.Fatalf("confidence mismatch: got %v", got.Confidence)
	}
}

func TestLoadOrCreate_NullDetectorsMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")

	// Valid JSON but Detectors field is null.
	raw := `{"detectors": null, "last_prune": "2025-01-01T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	l, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if l.Detectors == nil {
		t.Fatal("expected Detectors map to be initialized after null unmarshal")
	}
}

// ---- WriteAtomic ----

func TestWriteAtomic_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deeper", "ledger.json")

	l := ledgerWith("d", nil)
	if err := WriteAtomic(path, l); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file at %s: %v", path, err)
	}
}

func TestWriteAtomic_NoTmpLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")

	if err := WriteAtomic(path, ledgerWith("d", nil)); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected no .tmp file, got err=%v", err)
	}
}

func TestWriteAtomic_ContentsAreValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")

	l := ledgerWith("d", []MotionEvent{
		{Timestamp: time.Now().UTC(), Confidence: 0.5},
	})
	if err := WriteAtomic(path, l); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var roundtrip Ledger
	if err := json.Unmarshal(data, &roundtrip); err != nil {
		t.Fatalf("file contents are not valid JSON: %v", err)
	}
}

// ---- RecordEvent ----

func TestRecordEvent_NewDetector(t *testing.T) {
	l := &Ledger{Detectors: map[string]*DetectorLedger{}}
	RecordEvent(l, "det-new", 0.42)

	d, ok := l.Detectors["det-new"]
	if !ok {
		t.Fatal("expected bucket for det-new")
	}
	if len(d.Events) != 1 {
		t.Fatalf("event count: %d", len(d.Events))
	}
	if d.Events[0].Confidence != 0.42 {
		t.Fatalf("confidence: %v", d.Events[0].Confidence)
	}
	if d.Events[0].Timestamp.Location() != time.UTC {
		t.Fatalf("timestamp not UTC: %v", d.Events[0].Timestamp.Location())
	}
}

func TestRecordEvent_AppendsAndPreservesOrder(t *testing.T) {
	l := &Ledger{Detectors: map[string]*DetectorLedger{}}
	RecordEvent(l, "det", 0.1)
	RecordEvent(l, "det", 0.2)
	RecordEvent(l, "det", 0.3)

	events := l.Detectors["det"].Events
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	for i, want := range []float64{0.1, 0.2, 0.3} {
		if events[i].Confidence != want {
			t.Fatalf("event %d: got %v want %v", i, events[i].Confidence, want)
		}
	}
}

// ---- Prune ----

func TestPrune_DropsOldKeepsRecent(t *testing.T) {
	now := time.Now().UTC()
	l := ledgerWith("det", []MotionEvent{
		{Timestamp: now.Add(-72 * time.Hour), Confidence: 0.1},   // drop
		{Timestamp: now.Add(-1 * time.Hour), Confidence: 0.2},    // keep
		{Timestamp: now.Add(-30 * time.Minute), Confidence: 0.3}, // keep
	})

	Prune(l, 48*time.Hour)

	events := l.Detectors["det"].Events
	if len(events) != 2 {
		t.Fatalf("expected 2 surviving events, got %d", len(events))
	}
	// Order preserved.
	if events[0].Confidence != 0.2 || events[1].Confidence != 0.3 {
		t.Fatalf("survivors out of order: %+v", events)
	}
}

func TestPrune_UpdatesLastPrune(t *testing.T) {
	l := ledgerWith("det", nil)
	before := time.Now().UTC()
	Prune(l, time.Hour)
	if l.LastPrune.Before(before) {
		t.Fatalf("LastPrune not advanced: %v vs %v", l.LastPrune, before)
	}
}

func TestPrune_EmptyLedgerNoPanic(t *testing.T) {
	l := &Ledger{Detectors: map[string]*DetectorLedger{}}
	Prune(l, time.Hour) // must not panic
}

// ---- Clear ----

func TestClearAll(t *testing.T) {
	now := time.Now().UTC()
	l := &Ledger{Detectors: map[string]*DetectorLedger{
		"a": {Events: []MotionEvent{{Timestamp: now, Confidence: 0.1}}},
		"b": {Events: []MotionEvent{{Timestamp: now, Confidence: 0.2}}},
	}}

	ClearAll(l)

	if len(l.Detectors) != 2 {
		t.Fatalf("expected detector keys preserved, got %d", len(l.Detectors))
	}
	for name, d := range l.Detectors {
		if len(d.Events) != 0 {
			t.Fatalf("detector %s still has events", name)
		}
	}
}

func TestClearDetector_Known(t *testing.T) {
	now := time.Now().UTC()
	l := ledgerWith("a", []MotionEvent{{Timestamp: now, Confidence: 0.1}})

	if err := ClearDetector(l, "a"); err != nil {
		t.Fatalf("ClearDetector: %v", err)
	}
	if len(l.Detectors["a"].Events) != 0 {
		t.Fatal("events not cleared")
	}
}

func TestClearDetector_Unknown(t *testing.T) {
	l := ledgerWith("a", nil)
	if err := ClearDetector(l, "missing"); err == nil {
		t.Fatal("expected error for unknown detector")
	}
}

// ---- Readings ----

func TestToReadings_Shape(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	l := &Ledger{
		LastPrune: now,
		Detectors: map[string]*DetectorLedger{
			"a": {Events: []MotionEvent{
				{Timestamp: now, Confidence: 0.5},
				{Timestamp: now.Add(time.Second), Confidence: 0.6},
			}},
		},
	}

	r := ToReadings(l)

	if r["last_prune"] != now.Format(time.RFC3339) {
		t.Fatalf("last_prune: %v", r["last_prune"])
	}
	ts, ok := r["a"].([]interface{})
	if !ok {
		t.Fatalf("a is not []interface{}: %T", r["a"])
	}
	if len(ts) != 2 {
		t.Fatalf("timestamp slice length: %d", len(ts))
	}
	for _, v := range ts {
		if _, ok := v.(string); !ok {
			t.Fatalf("timestamp not string: %T", v)
		}
	}
	if r["a_count"] != 2 {
		t.Fatalf("a_count: %v", r["a_count"])
	}
}

func TestToReadings_Empty(t *testing.T) {
	l := &Ledger{
		LastPrune: time.Now().UTC(),
		Detectors: map[string]*DetectorLedger{},
	}
	r := ToReadings(l)
	if _, ok := r["last_prune"].(string); !ok {
		t.Fatalf("last_prune missing or wrong type: %T", r["last_prune"])
	}
}

func TestToFullReadings_IncludesConfidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	l := ledgerWith("a", []MotionEvent{{Timestamp: now, Confidence: 0.77}})

	r := ToFullReadings(l)

	detectors, ok := r["detectors"].(map[string][]map[string]interface{})
	if !ok {
		t.Fatalf("detectors wrong type: %T", r["detectors"])
	}
	events := detectors["a"]
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0]["confidence"] != 0.77 {
		t.Fatalf("confidence: %v", events[0]["confidence"])
	}
	if _, ok := events[0]["timestamp"].(string); !ok {
		t.Fatalf("timestamp not string: %T", events[0]["timestamp"])
	}
}
