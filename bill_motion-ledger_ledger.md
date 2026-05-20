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
| `retention_hours` | int | no | `48` | Events older than this are pruned on every `poll_for_motion`. |

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

The module does not poll on its own. Trigger `poll_for_motion` externally —
typical patterns:

- **Cron / systemd timer** on the same machine, calling the module via the
  Viam CLI or a small script.
- **A Viam automation** that fires `DoCommand` on a schedule.
- **Another resource** (e.g., a wrapping sensor) that polls on its own cadence.

## Example workflow

1. Wire up two motion detectors and their cameras on a machine.
2. Add this sensor with both detectors listed.
3. Run `poll_for_motion` every 30 seconds via cron.
4. From a dashboard or automation, periodically call `query_motion` with the
   window you care about and react to `has_motion`.
