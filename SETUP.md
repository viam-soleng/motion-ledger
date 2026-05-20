# Setup guide

End-to-end walkthrough for getting `viam-soleng:motion-ledger:ledger` running on a
Viam machine. If you just need the configuration reference, see
[README.md](README.md) and [viam-soleng_motion-ledger_ledger.md](viam-soleng_motion-ledger_ledger.md).

This guide assumes you're consuming the module from the Viam registry. If
the registry has no published version yet, the steps below will fail at the
"add module" stage — a maintainer needs to publish one first via
`viam module upload`.

## Prerequisites

Before configuring this module, make sure your machine has all of the
following already working:

1. **A Viam machine claimed in [app.viam.com](https://app.viam.com).**
   The machine should be online and reachable; you can see logs flowing in
   the **LOGS** tab.

2. **A camera component.** Any camera that streams frames will do — built-in
   webcam, USB camera, RTSP, whatever. Confirm it works by viewing the live
   feed in the **CONTROL** tab.

3. **A vision-service motion detector bound to that camera.** The simplest
   choice is `viam:vision:motion-detector`. Its config must include a
   `camera_name` attribute pointing at the camera from step 2. Confirm it
   works by opening the vision service's preview in the **CONTROL** tab and
   moving in front of the camera — you should see motion detections appear.

   Example vision service config block:

   ```json
   {
     "name": "vision-1",
     "api": "rdk:service:vision",
     "model": "viam:vision:motion-detector",
     "attributes": {
       "camera_name": "camera-1",
       "sensitivity": 0.9,
       "min_box_size": 2000
     }
   }
   ```

   If the vision service doesn't emit motion detections on its own, this
   module has nothing to record.

## Step 1 — Add the module

In app.viam.com → your machine → **CONFIGURE** tab:

1. Click **+** (top left of the configure panel) → **Module**.
2. Search the registry for `viam-soleng:motion-ledger`.
3. Click **Add module**.
4. Save the config.

This adds an entry to your machine's `modules` array. Viam-agent will
download the module package on the next reconfigure.

## Step 2 — Add the sensor component

Still in **CONFIGURE**, click **+** → **Component** → **sensor**, then:

1. Pick the model `viam-soleng:motion-ledger:ledger`.
2. Give it a name (e.g. `motion-ledger`).
3. Switch to the **JSON** view of the component to set its attributes.

Use the following attribute block, substituting your vision-service and
camera names from the prerequisites:

```json
"attributes": {
  "ledger_path": "/var/lib/viam/motion-events.json",
  "retention_hours": 48,
  "poll_interval_seconds": 30,
  "motion_detectors": [
    { "name": "vision-1", "camera": "camera-1" }
  ]
}
```

Field reference:

| Field | Required | Default | Notes |
|---|---|---|---|
| `motion_detectors` | yes | — | List of `{name, camera}` pairs. `name` must match a configured vision service on this machine. `camera` is the camera the vision service should pull frames from. |
| `ledger_path` | no | `/var/lib/viam/motion-events.json` | Where the JSON ledger is written. Parent directories are created on first write. |
| `retention_hours` | no | `48` | Events older than this are pruned on every poll. |
| `poll_interval_seconds` | no | `0` (off) | When `> 0`, the module polls all detectors on its own every N seconds. `0` means "external trigger only". |

Save the config. Within a few seconds the **LOGS** tab should show a line
like:

```
Successfully constructed resource ... viam-soleng:motion-ledger:ledger
```

If `poll_interval_seconds > 0`, you'll also see:

```
starting internal polling   interval 30s
```

## Step 3 — Verify it's working

Open the **CONTROL** tab and find your `motion-ledger` sensor.

### Confirm `Readings()` works

Set the refresh dropdown to **"Refresh every 5 seconds"**. You should see:

```
last_prune         "2026-xx-xxTxx:xx:xxZ"
vision-1           []      (or list of timestamps if motion has occurred)
vision-1_count    0       (or count)
```

`last_prune` should advance every poll interval. If it's frozen, see
[Troubleshooting](#troubleshooting).

### Trigger a manual poll

Find the **DoCommand** input on the sensor's CONTROL panel and run:

```json
{ "poll_for_motion": true }
```

Expected response:

```json
{ "status": "ok" }
```

After this, `Readings()` will show the new event if motion was happening at
the moment of the call.

### Query a time window

```json
{
  "command": "query_motion",
  "from": "2026-01-01T00:00:00Z",
  "to":   "2099-01-01T00:00:00Z"
}
```

Returns `{"has_motion": <bool>, "count": <int>}`. Useful for the
"did anything happen between T1 and T2?" use case.

See [README.md](README.md#commands) for the full DoCommand surface
(`clear_ledger`, `dump_ledger`, etc.).

## Triggering polls

Two modes, which coexist:

- **Internal** — set `poll_interval_seconds` to a positive number in the
  component config. The module spawns a background goroutine that polls
  every N seconds. **Recommended for most deployments.**
- **External** — leave `poll_interval_seconds` at 0 and call
  `DoCommand({"poll_for_motion": true})` from cron, a Viam automation,
  another resource, or an SDK script. Useful if you need precise control
  over when polls happen.

When both are configured, manual polls still work — they share a mutex with
the internal goroutine so they cannot run concurrently.

## Troubleshooting

These are the real errors you'll hit if something is misconfigured.

### `resource build error: unknown resource type ... not registered`

The module didn't load. Causes, most-common first:

- **The module entry exists but the registry has no published version
  matching `latest`.** Error message includes
  `No versions that fit constraint "latest" for module`. A maintainer
  needs to publish a build via `viam module upload`.
- **The module is configured as `type: local` but the binary path is
  wrong.** Error message includes
  `module executable path error: stat ...: no such file or directory`.
  Fix the `executable_path` in the modules entry.
- **The binary exists but isn't executable, or the path points at a
  directory instead of a file.** Error message includes
  `fork/exec ...: permission denied`. On Linux, `execve()` on a
  directory returns the same `EACCES` as a missing executable bit, so
  check both:
  `ls -la <path>` should show `-rwx...` (file, not directory) and the
  `x` bits should be set.

### `modular resource config validation error: bluetooth: base_component is required` (or similar)

Unrelated to motion-ledger — this is a different module on the same
machine complaining about its own config. Ignore unless it's blocking
something you care about.

### `Source camera must be provided as 'cam_name' or 'camera_name'`

Your vision service is misconfigured, not motion-ledger.
`viam:vision:motion-detector` needs `camera_name` (or `cam_name`,
depending on version) in its attributes. Fix the vision service config
and motion-ledger will start receiving valid detections.

### `rename .tmp : no such file or directory`

The ledger path is empty. This shouldn't happen with the current build —
defaults are applied in the constructor — but if you see it, set
`ledger_path` explicitly in the component attributes to work around it.

### Readings work but `vision-1_count` never increases

Possibilities:

- **Polls aren't running.** Check that `last_prune` is advancing on every
  poll interval. If it's frozen, polls are failing — look for log lines
  matching `motion poll: detector error` or `internal poll failed`.
- **The vision service isn't emitting detections labeled `motion`.** The
  ledger only records detections whose label (case-insensitive) is
  `motion` and whose score is `> 0`. Open the vision service preview in
  the CONTROL tab and confirm motion detections appear there before
  expecting them in the ledger.
- **The app UI is showing cached data.** Set the refresh dropdown to
  **"Refresh every 5 seconds"** rather than **Manual refresh**.

### `last_prune` advancing but motion is never recorded even though I can see motion in the camera preview

The vision service isn't returning a `motion` label, or it's returning it
with score `0`. Open the vision service's CONTROL panel and confirm what
labels and scores it emits. If labels are different (e.g. `"object"`,
`"person"`), this module won't pick them up — it's specifically looking
for the `"motion"` label.

### After updating the binary on a local-module install, behavior hasn't changed

Replacing the binary file on disk doesn't restart the running module
process — viam-server keeps using the in-memory copy from when it first
launched it. To force a re-exec, restart viam-agent:

```
sudo systemctl restart viam-agent
```

This causes ~10–20 seconds of machine downtime. After the restart, the
new binary takes over and you should see fresh `starting internal
polling` lines in the logs (if `poll_interval_seconds > 0`).

## Verifying the on-disk ledger directly

If you have shell access to the machine, the ledger is just a JSON file:

```
sudo cat /var/lib/viam/motion-events.json
```

Useful for confirming polls are actually writing, even when the UI shows
stale data.
