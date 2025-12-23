// motionledger implements a sensor that periodically polls one or more
// motion-detector vision services and persists motion events to disk.
package motionledger

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
	"strings"

	sensor "go.viam.com/rdk/components/sensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"

	"motionledger/utils/ledger"
	"motionledger/utils/motion"
)

// Ledger is the registered model for the motion ledger sensor.
// Namespace: bill, Family: motion-ledger, Model: ledger
var (
	Ledger           = resource.NewModel("bill", "motion-ledger", "ledger")
	// Reserved for future command or API surface expansion.
	errUnimplemented = errors.New("unimplemented")
)

func init() {
	resource.RegisterComponent(sensor.API, Ledger,
		resource.Registration[sensor.Sensor, *Config]{
			Constructor: newMotionLedgerLedger,
		},
	)
}

// Config defines runtime configuration for the motion ledger sensor.
type Config struct {
	// Filesystem path where motion events are persisted
	LedgerPath string `json:"ledger_path,omitempty"`

	// Retention window for motion events (in hours)
	RetentionHours int `json:"retention_hours,omitempty"`

	// Names of motion detector vision services to poll
	MotionDetectors []string `json:"motion_detectors"`
}

// Validate sets defaults and declares required motion detector dependencies.
// The module will fail to start unless all configured detectors are present.
func (cfg *Config) Validate(path string) ([]string, []string, error) {
	if cfg.LedgerPath == "" {
		cfg.LedgerPath = "/var/lib/viam/motion-events.json"
	}

	if cfg.RetentionHours <= 0 {
		cfg.RetentionHours = 48
	}

	if len(cfg.MotionDetectors) == 0 {
		return nil, nil, fmt.Errorf(
			"%s.motion_detectors must contain at least one detector name",
			path,
		)
	}

	// REQUIRED deps: motion detectors must exist
	required := make([]string, 0, len(cfg.MotionDetectors))
	for _, name := range cfg.MotionDetectors {
		required = append(required, name)
	}

	return required, nil, nil
}

// motionLedgerLedger implements sensor.Sensor and maintains a persistent,
// append-only ledger of motion events reported by vision services.
type motionLedgerLedger struct {
	resource.AlwaysRebuild

	name   resource.Name
	logger logging.Logger
	cfg    *Config

	// Resolved dependencies (motion detector services)
	deps resource.Dependencies

	// In-memory representation of the on-disk motion ledger
	ledger *ledger.Ledger

	// Guards ledger access and mutation
	mu sync.RWMutex

	// Lifecycle cancellation
	cancelCtx  context.Context
	cancelFunc func()
}

// newMotionLedgerLedger is the resource constructor invoked by the RDK.
// It parses config and delegates to NewLedger.
func newMotionLedgerLedger(ctx context.Context, deps resource.Dependencies, rawConf resource.Config, logger logging.Logger) (sensor.Sensor, error) {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		return nil, err
	}

	return NewLedger(ctx, deps, rawConf.ResourceName(), conf, logger)
}

// NewLedger loads or creates the motion ledger and ensures entries
// exist for all configured detectors.
func NewLedger(
	ctx context.Context,
	deps resource.Dependencies,
	name resource.Name,
	conf *Config,
	logger logging.Logger,
) (sensor.Sensor, error) {

	cancelCtx, cancelFunc := context.WithCancel(context.Background())

	l, err := ledger.LoadOrCreate(conf.LedgerPath)
	if err != nil {
		return nil, err
	}

	// Ensure ledger entries exist for all configured detectors
	for _, name := range conf.MotionDetectors {
		if _, ok := l.Detectors[name]; !ok {
			l.Detectors[name] = &ledger.DetectorLedger{
				Events: []ledger.MotionEvent{},
			}
		}
	}

	s := &motionLedgerLedger{
		name:       name,
		logger:     logger,
		cfg:        conf,
		deps:       deps,
		ledger:     l,
		cancelCtx:  cancelCtx,
		cancelFunc: cancelFunc,
	}

	return s, nil
}

func (s *motionLedgerLedger) Name() resource.Name {
	return s.name
}

// Readings returns a summarized view of the motion ledger suitable
// for telemetry and dashboards.
func (s *motionLedgerLedger) Readings(ctx context.Context, extra map[string]interface{}) (map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a summarized, in-memory view of the ledger
	return ledger.ToReadings(s.ledger), nil
}

// DoCommand supports administrative and polling commands:
// - poll_for_motion: query detectors and record events
// - clear_ledger: clear all or per-detector history
// - dump_ledger: return full raw ledger contents
// - query_motion: count motion events in a [from,to] window, optionally scoped to one detector
func (s *motionLedgerLedger) DoCommand(
	ctx context.Context,
	cmd map[string]interface{},
) (map[string]interface{}, error) {

	// New command surface: {"command":"query_motion","from":"...","to":"...","vision_service":"vision-1"}
	if c, ok := cmd["command"]; ok {
		if cs, ok2 := c.(string); ok2 && cs == "query_motion" {
			return s.handleQueryMotion(ctx, cmd)
		}
	}
	// Back-compat alternate: {"query_motion":true,"from":"...","to":"...","vision_service":"vision-1"}
	if _, ok := cmd["query_motion"]; ok {
		return s.handleQueryMotion(ctx, cmd)
	}

	// handlePollForMotion queries each configured motion detector,
	// records any positive-confidence motion events, prunes old data,
	// and atomically persists the updated ledger to disk.
	if _, ok := cmd["poll_for_motion"]; ok {
		return s.handlePollForMotion(ctx)
	}

	// handleClearLedger clears motion history either globally or
	// for a single detector and persists the result.
	if v, ok := cmd["clear_ledger"]; ok {
		return s.handleClearLedger(ctx, v)
	}

	if _, ok := cmd["dump_ledger"]; ok {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return ledger.ToFullReadings(s.ledger), nil
	}

	return nil, fmt.Errorf("unknown command: %v", cmd)
}

// Close terminates any background activity and releases resources.
func (s *motionLedgerLedger) Close(context.Context) error {
	s.cancelFunc()
	return nil
}

func (s *motionLedgerLedger) handlePollForMotion(
	ctx context.Context,
) (map[string]interface{}, error) {

	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Debug("motion poll started")

	detectors, err := motion.ResolveConfiguredDetectors(
		s.deps,
		s.cfg.MotionDetectors,
	)
	if err != nil {
		s.logger.Warn("motion poll failed: could not resolve detectors")
		ledger.RecordEvent(s.ledger, "error", 0.0)
		return nil, err
	}

	for name, detector := range detectors {
		conf, err := motion.QueryMotion(ctx, detector)
		if err != nil {
			s.logger.Error(
				"motion poll: detector error",
				"detector", name,
				"error", err,
			)
			continue
		}

		if conf > 0 {
			s.logger.Debugf(
				"motion detected (%s): confidence=%.3f",
				name,
				conf,
			)
			ledger.RecordEvent(s.ledger, name, conf)
		} else {
			s.logger.Debugf(
				"no motion detected (%s)",
				name,
			)
		}
	}

	s.logger.Debug("pruning old events")

	ledger.Prune(
		s.ledger,
		time.Duration(s.cfg.RetentionHours)*time.Hour,
	)

	// Clean up detectors that are no longer configured and have no remaining events
	configured := make(map[string]struct{}, len(s.cfg.MotionDetectors))
	for _, name := range s.cfg.MotionDetectors {
		configured[name] = struct{}{}
	}

	// Keep any special/system buckets you rely on
	configured["error"] = struct{}{}

	for name, dl := range s.ledger.Detectors {
		if _, ok := configured[name]; ok {
			continue
		}
		if dl == nil || len(dl.Events) == 0 {
			delete(s.ledger.Detectors, name)
		}
	}


	if err := ledger.WriteAtomic(s.cfg.LedgerPath, s.ledger); err != nil {
		return nil, err
	}

	s.logger.Debug("motion poll finished")

	return map[string]interface{}{
		"status": "ok",
	}, nil
}

func (s *motionLedgerLedger) handleClearLedger(
	ctx context.Context,
	value interface{},
) (map[string]interface{}, error) {

	s.mu.Lock()
	defer s.mu.Unlock()

	target, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf(
			"clear_ledger must be a string: 'all' or detector name",
		)
	}

	switch target {
	case "all":
		ledger.ClearAll(s.ledger)

	default:
		if err := ledger.ClearDetector(s.ledger, target); err != nil {
			return nil, err
		}
	}

	if err := ledger.WriteAtomic(s.cfg.LedgerPath, s.ledger); err != nil {
		return nil, err
	}

	s.logger.Info(
		"ledger cleared",
		"scope", target,
	)

	return map[string]interface{}{
		"status": "cleared",
		"scope":  target,
	}, nil
}

// handleQueryMotion counts motion events within an inclusive [from,to] window.
// Inputs:
//   - from: RFC3339/RFC3339Nano timestamp string
//   - to:   RFC3339/RFC3339Nano timestamp string
//   - vision_service (optional): detector name to scope to; if omitted counts across all detectors
//
// Output:
//   - has_motion: bool
//   - count: int
func (s *motionLedgerLedger) handleQueryMotion(
	ctx context.Context,
	cmd map[string]interface{},
) (map[string]interface{}, error) {

	_ = ctx // reserved for future, keeps signature consistent

	fromStr, ok := cmd["from"].(string)
	if !ok || fromStr == "" {
		return nil, fmt.Errorf("query_motion requires string field 'from'")
	}
	toStr, ok := cmd["to"].(string)
	if !ok || toStr == "" {
		return nil, fmt.Errorf("query_motion requires string field 'to'")
	}

	parseTS := func(v string) (time.Time, error) {
		// Accept a filename-safe variant: 2025-12-23_17-59-58Z
		if strings.Contains(v, "_") && strings.HasSuffix(v, "Z") && !strings.Contains(v, "T") {
			parts := strings.SplitN(v, "_", 2)
			if len(parts) == 2 {
				// 17-59-58Z -> 17:59:58Z
				tpart := strings.ReplaceAll(parts[1], "-", ":")
				v = parts[0] + "T" + tpart
			}
		}

		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t, nil
		}
		return time.Parse(time.RFC3339, v)
	}


	fromTS, err := parseTS(fromStr)
	if err != nil {
		return nil, fmt.Errorf("invalid 'from' timestamp %q: %w", fromStr, err)
	}
	toTS, err := parseTS(toStr)
	if err != nil {
		return nil, fmt.Errorf("invalid 'to' timestamp %q: %w", toStr, err)
	}
	if fromTS.After(toTS) {
		return nil, fmt.Errorf("'from' must be <= 'to' (from=%q to=%q)", fromStr, toStr)
	}

	visionService := ""
	if v, ok := cmd["vision_service"]; ok {
		if vs, ok2 := v.(string); ok2 {
			visionService = vs
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0

	within := func(ts time.Time) bool {
		// inclusive bounds: from <= ts <= to
		if ts.Before(fromTS) {
			return false
		}
		if ts.After(toTS) {
			return false
		}
		return true
	}

	if visionService != "" {
		dl, ok := s.ledger.Detectors[visionService]
		if !ok {
			return nil, fmt.Errorf("unknown vision_service %q (not in ledger)", visionService)
		}
		for _, ev := range dl.Events {
			if within(ev.Timestamp) {
				count++
			}
		}
	} else {
		for _, dl := range s.ledger.Detectors {
			for _, ev := range dl.Events {
				if within(ev.Timestamp) {
					count++
				}
			}
		}
	}

	return map[string]interface{}{
		"has_motion": count > 0,
		"count":      count,
	}, nil
}
