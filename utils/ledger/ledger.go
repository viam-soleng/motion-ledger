// Package ledger provides a lightweight, file-backed ledger for recording,
// pruning, and exporting motion detection events.
package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MotionEvent represents a single motion occurrence at a point in time.
type MotionEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	Confidence float64   `json:"confidence"`
}

// DetectorLedger stores motion events for a single detector.
type DetectorLedger struct {
	Events []MotionEvent `json:"events"`
}

// Ledger is the top-level container persisted to disk.
// It maintains per-detector event history and pruning metadata.
type Ledger struct {
	Detectors map[string]*DetectorLedger `json:"detectors"`
	LastPrune time.Time                  `json:"last_prune"`
}

// LoadOrCreate loads a ledger from disk or initializes a new one.
// If the file is missing or unreadable, a new ledger is created.
// Corrupt files are renamed with a ".corrupt" suffix and replaced.
func LoadOrCreate(path string) (*Ledger, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Ledger{
				Detectors: make(map[string]*DetectorLedger),
				LastPrune: time.Now().UTC(),
			}, nil
		}
		return nil, err
	}

	var l Ledger
	if err := json.Unmarshal(data, &l); err != nil {
		// Preserve corrupt file for offline inspection
		_ = os.Rename(path, path+".corrupt")
		return &Ledger{
			Detectors: make(map[string]*DetectorLedger),
			LastPrune: time.Now().UTC(),
		}, nil
	}

	if l.Detectors == nil {
		l.Detectors = make(map[string]*DetectorLedger)
	}

	return &l, nil
}

// WriteAtomic persists the ledger to disk using a write-then-rename
// strategy to avoid partial writes and corruption.
func WriteAtomic(path string, l *Ledger) error {
	_ = os.MkdirAll(filepath.Dir(path), 0755)

	tmp := path + ".tmp"

	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

// RecordEvent appends a motion event for the given detector using
// the current UTC timestamp.
func RecordEvent(
	l *Ledger,
	detectorName string,
	confidence float64,
) {

	if _, ok := l.Detectors[detectorName]; !ok {
		l.Detectors[detectorName] = &DetectorLedger{}
	}

	l.Detectors[detectorName].Events = append(
		l.Detectors[detectorName].Events,
		MotionEvent{
			Timestamp:  time.Now().UTC(),
			Confidence: confidence,
		},
	)
}

// Prune removes events older than the retention window.
// Events are filtered in-place to avoid additional allocations.
func Prune(l *Ledger, retention time.Duration) {
	cutoff := time.Now().UTC().Add(-retention)

	for _, d := range l.Detectors {
		kept := d.Events[:0]
		for _, e := range d.Events {
			if e.Timestamp.After(cutoff) {
				kept = append(kept, e)
			}
		}
		d.Events = kept
	}

	l.LastPrune = time.Now().UTC()
}

// ToReadings returns a compact, proto-safe view of the ledger
// intended for sensor.Readings and dashboards.
func ToReadings(l *Ledger) map[string]interface{} {
	out := make(map[string]interface{})

	out["last_prune"] = l.LastPrune.UTC().Format(time.RFC3339)

	for name, d := range l.Detectors {
		// timestamps only — proto safe
		timestamps := make([]interface{}, 0, len(d.Events))
		for _, e := range d.Events {
			timestamps = append(
				timestamps,
				e.Timestamp.UTC().Format(time.RFC3339),
			)
		}
		out[name] = timestamps
		out[name+"_count"] = len(d.Events)
	}

	return out
}

// ClearAll removes all motion events from all detectors.
func ClearAll(l *Ledger) {
	for _, d := range l.Detectors {
		d.Events = nil
	}
}

// ClearDetector removes all motion events for a single detector.
// Returns an error if the detector is unknown.
func ClearDetector(l *Ledger, detectorName string) error {
	d, ok := l.Detectors[detectorName]
	if !ok {
		return fmt.Errorf("detector %q does not exist", detectorName)
	}

	d.Events = nil
	return nil
}

// ToFullReadings returns the complete ledger including timestamps
// and confidence values, intended for debugging and inspection.
func ToFullReadings(l *Ledger) map[string]interface{} {
	out := make(map[string]interface{})

	detectors := make(map[string][]map[string]interface{})

	for name, d := range l.Detectors {
		events := make([]map[string]interface{}, 0, len(d.Events))
		for _, e := range d.Events {
			events = append(events, map[string]interface{}{
				"timestamp":  e.Timestamp.UTC().Format(time.RFC3339),
				"confidence": e.Confidence,
			})
		}
		detectors[name] = events
	}

	out["detectors"] = detectors
	out["last_prune"] = l.LastPrune.UTC().Format(time.RFC3339)
	return out
}
