# `bill:motion-ledger:ledger`

A Viam **sensor** that polls one or more vision-service motion detectors and
maintains a persistent, on-device JSON ledger of motion events. The ledger
can be queried for motion within a time window via `DoCommand`, without
round-tripping through Viam Data Management.

See [README.md](README.md) for the full overview, file-format details, and
known limitations.

## Configuration

```json
{
  "ledger_path": "/var/lib/viam/motion-events.json",
  "retention_hours": 48,
  "poll_interval_seconds": 30,
  "motion_detectors": [
    { "name": "vision-front-door", "camera": "cam-front-door" },
    { "name": "vision-driveway",   "camera": "cam-driveway"   }
  ]
}
```

### Attributes

| Name | Type | Required | Default | Description |
|---|---|---|---|---|
| `motion_detectors` | array of object | **yes** | — | Detectors to poll. Each entry has `name` (a vision-service resource name on this machine) and `camera` (the camera name passed to `DetectionsFromCamera`). |
| `motion_detectors[].name` | string | **yes** | — | Vision service to query. Declared as a required dependency. |
| `motion_detectors[].camera` | string | **yes** | — | Camera the vision service should pull frames from. Resolved by the vision service, not by this module. |
| `ledger_path` | string | no | `/var/lib/viam/motion-events.json` | Disk location for the JSON ledger. Parent dirs are created on first write. |
| `retention_hours` | int | no | `48` | Events older than this are pruned on every poll. |
| `poll_interval_seconds` | int | no | `0` (off) | When `> 0`, the module spawns a background goroutine that calls `poll_for_motion` every N seconds. `0` disables internal polling — the operator must trigger via `DoCommand`. Negative values are rejected. |

Empty `name` or `camera` fields are rejected at validation time, as are
duplicate `name`s.

## `DoCommand` reference

| Command | Body | Response |
|---|---|---|
| Poll detectors and record events | `{ "poll_for_motion": true }` | `{ "status": "ok" }` |
| Count events in a window | `{ "command": "query_motion", "from": "<RFC3339>", "to": "<RFC3339>", "vision_service": "<name>" }` (`vision_service` optional) | `{ "has_motion": <bool>, "count": <int> }` |
| Clear all history | `{ "clear_ledger": "all" }` | `{ "status": "cleared", "scope": "all" }` |
| Clear one detector | `{ "clear_ledger": "<detector-name>" }` | `{ "status": "cleared", "scope": "<detector-name>" }` |
| Dump full raw ledger | `{ "dump_ledger": true }` | Full ledger contents (debug only) |

`query_motion` accepts both `{"command": "query_motion", ...}` and a back-compat
`{"query_motion": true, ...}` form. The window is inclusive on both ends.
Timestamps accept RFC3339, RFC3339Nano, and a filename-safe variant such as
`2026-05-19_17-59-58Z` (interpreted as `2026-05-19T17:59:58Z`).

## `Readings()` shape

```json
{
  "last_prune": "<RFC3339 timestamp>",
  "<detector-name>": ["<RFC3339>", "..."],
  "<detector-name>_count": <int>
}
```

One pair of `<name>` / `<name>_count` keys per detector. The `<name>` value
is a list of event timestamps; the count is the length of that list.

## Triggering polling

Two modes, which can coexist:

- **Internal** — set `poll_interval_seconds` to a positive integer. The module
  spawns a background goroutine that polls all detectors on that cadence.
  Default is `0`, meaning off.
- **External** — call `DoCommand({"poll_for_motion": true})` from anywhere
  (cron, a Viam automation, another resource, an SDK script). Works even when
  internal polling is also enabled.

If both are configured, manual polls and the interval-driven polls share the
same mutex, so they cannot run concurrently.

## Example workflow

1. Wire up two motion detectors and their cameras on a machine.
2. Add this sensor with both detectors listed, and set
   `poll_interval_seconds: 30`.
3. From a dashboard or automation, periodically call `query_motion` with the
   window you care about and react to `has_motion`.
