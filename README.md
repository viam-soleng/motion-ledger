# motion-ledger

A Viam **sensor** component that polls one or more vision-service motion
detectors and maintains a persistent, queryable on-device ledger of motion
events. Useful when you want a cheap, local answer to "did any motion happen
between T1 and T2?" without depending on Viam Data Management or a round trip
to the cloud.

## Models

- `bill:motion-ledger:ledger` — see [bill_motion-ledger_ledger.md](bill_motion-ledger_ledger.md).

## Setup

If you're getting this running on a machine for the first time, see
[SETUP.md](SETUP.md) for an end-to-end walkthrough — prerequisites,
configuration, verification, and troubleshooting for the errors that
commonly come up the first time around. The rest of this README is the
configuration and behavior reference.

## How it works

On every poll the module:

1. Queries every configured vision service with `DetectionsFromCamera` against
   the camera you bound it to.
2. Records any detection whose label is `motion` (case-insensitive) and whose
   score is positive, tagged with the detector name and a UTC timestamp.
3. Prunes events older than the retention window.
4. Atomically persists the updated ledger to disk
   (write-to-`.tmp` → `rename`). Corrupt files on load are quarantined as
   `<path>.corrupt` and replaced with a fresh ledger.

A poll runs in either of two ways:

- **External trigger** — anything that can call `DoCommand({"poll_for_motion": true})`:
  cron, a Viam automation, another resource, an SDK script. This is the default.
- **Internal interval** — set `poll_interval_seconds` to a positive integer in
  the component config. The module spawns a background goroutine that polls
  on that cadence. The two modes can coexist; manual `DoCommand` polls still
  work when internal polling is on.

## Configuration

```json
{
  "name": "motion-ledger-1",
  "type": "sensor",
  "model": "bill:motion-ledger:ledger",
  "attributes": {
    "ledger_path": "/var/lib/viam/motion-events.json",
    "retention_hours": 48,
    "poll_interval_seconds": 30,
    "motion_detectors": [
      { "name": "vision-front-door", "camera": "cam-front-door" },
      { "name": "vision-driveway",   "camera": "cam-driveway"   }
    ]
  },
  "depends_on": []
}
```

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `motion_detectors` | array of `{name, camera}` | **yes** | — | Each `name` must be a configured vision service on the same machine. Each `camera` is the camera the vision service should pull frames from. Duplicate `name`s are rejected. |
| `ledger_path` | string | no | `/var/lib/viam/motion-events.json` | Disk location of the JSON ledger. Parent directories are created if missing. |
| `retention_hours` | int | no | `48` | Events older than this are pruned on every poll. |
| `poll_interval_seconds` | int | no | `0` (off) | When `> 0`, the module polls all detectors on its own every N seconds. `0` disables internal polling — operator must trigger via `DoCommand`. Negative values are rejected. |

Each configured detector `name` is declared as a required dependency, so the
module will fail to start (loudly) if any detector is missing. Cameras are
**not** declared as dependencies — they are resolved internally by the
vision service, which may bind to a remote camera.

## Commands

All commands go through `DoCommand`.

### `poll_for_motion`

Queries every configured detector, records motion events, prunes, and persists.

```json
{ "poll_for_motion": true }
```

Response:

```json
{ "status": "ok" }
```

Trigger this manually whenever you need an immediate poll, even when
`poll_interval_seconds` is set — the two mechanisms coexist.

### `query_motion`

Counts motion events in an inclusive `[from, to]` window, optionally scoped to
a single detector.

```json
{
  "command": "query_motion",
  "from": "2026-05-19T00:00:00Z",
  "to":   "2026-05-19T23:59:59Z",
  "vision_service": "vision-front-door"
}
```

`from` and `to` accept RFC3339 / RFC3339Nano, plus a filename-safe variant:
`2026-05-19_17-59-58Z` is interpreted as `2026-05-19T17:59:58Z`.

`vision_service` is optional; omit it to count across all detectors.

Response:

```json
{ "has_motion": true, "count": 17 }
```

An unknown `vision_service` returns an error rather than `count: 0`.

### `clear_ledger`

```json
{ "clear_ledger": "all" }
```

or

```json
{ "clear_ledger": "vision-front-door" }
```

Response:

```json
{ "status": "cleared", "scope": "all" }
```

### `dump_ledger`

Returns the full ledger contents — every event with timestamp and confidence —
intended for debugging.

```json
{ "dump_ledger": true }
```

## `Readings()`

Returns a compact, proto-safe summary intended for telemetry / dashboards:

```json
{
  "last_prune": "2026-05-19T15:00:00Z",
  "vision-front-door": ["2026-05-19T14:55:00Z", "2026-05-19T14:58:12Z"],
  "vision-front-door_count": 2,
  "vision-driveway": [],
  "vision-driveway_count": 0
}
```

## Ledger file format

The ledger is plain JSON, per-detector buckets:

```json
{
  "detectors": {
    "vision-front-door": {
      "events": [
        { "timestamp": "2026-05-19T14:55:00Z", "confidence": 0.92 }
      ]
    }
  },
  "last_prune": "2026-05-19T15:00:00Z"
}
```

Writes are atomic (write-`.tmp` → `rename`). If the file is corrupt when the
module starts, it is renamed to `<path>.corrupt` and a fresh ledger is created
in its place — the corrupt file is preserved for offline inspection.

## Known limitations

- **Polls are serial.** If a poll takes longer than `poll_interval_seconds`,
  the next tick fires immediately after the previous one returns rather than
  running concurrently. (`handlePollForMotion` already takes a mutex, so
  concurrent polls would serialize anyway.)
- **Time-based retention only.** No per-detector event cap. At 10 detectors
  firing at 1 Hz over a 48 h retention window, the file is ~130 MB worst case.
  If that's a concern, lower `retention_hours` or poll less often.
- **Local detectors only.** Detectors are looked up by simple resource name
  (no remote prefix), so the vision services must be configured on the same
  machine as this module.
- **Single `motion` label expected.** If your detector emits multiple
  positive-confidence detections per frame, only the first labeled `motion`
  is recorded.

## Development

```sh
make setup        # go mod tidy
go build ./...    # build everything
go vet ./...      # static analysis
go test ./...     # unit tests (utils/ledger)
make lint         # gofmt -s -w .
make module       # produces module.tar.gz for upload
```

Supported build targets: `linux/amd64`, `linux/arm64`, `darwin/arm64`.
