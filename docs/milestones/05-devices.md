# Milestone 5: field devices, bindings and lighting

Finished 2026-11-13. The protocol is in [docs/devices.md](../devices.md) and the code's structure in
[docs/architecture.md](../architecture.md#devices-m5).

## What works

Electronics join the field from the browser. No code change is needed for a new sensor, button or light.

- **Connecting.** Devices connect five ways, all into one registry:
  - MQTT, through a broker built into the FMS (ports 1883 and 8083, set on Setup → Settings);
  - a WebSocket;
  - plain HTTP with long-polled outputs;
  - USB serial, with ports chosen on Setup → Devices → USB serial ports;
  - virtual devices inside the FMS.

  The PLC appears as a read-only device. Every device announces a manifest of its channels.
- **Approving.** A new device waits on Setup → Devices until someone approves it. Add device creates one in
  advance and shows its token once, with a snippet for each SDK. Tokens are stored hashed. Blocking, deleting
  or re-tokening a device takes effect on its open connection.
- **Bindings.** Setup → Bindings says what each input does and what each output shows:
  - inputs can add to a score, start, abort or reset the field, commit, load the next match, or stop a robot or
    the field;
  - outputs show a look, chosen by expressions on the live match.

  Bindings are edited as cards that read as sentences, or as YAML, and the FMS checks them on every change. They
  are kept per game and per field profile; the active profile is chosen on Setup → Settings.
- **Lighting.** Setup → Lighting is the place to configure LEDs, stack lights and relays:
  - fixtures, with segments drawn on the strip;
  - a grid of scenes by field moment, with fall-through;
  - a live view, a preview, a test on the field, and identify.

  New fixtures start with useful scenes.
- **Safety.** A device bound to a stop must send a heartbeat every 200 ms. If it falls silent for a second,
  disconnects, or reports its stop circuit open, the stop is applied and a fault latches. The fault holds the
  stop until the device is back and someone clears it on Match Play. Device stops merge with the PLC's, and
  bound devices add their own start conditions.
- **Testing without hardware.**
  - Setup → Devices → Virtual devices offers seven templates with clickable inputs and live outputs.
  - `galaxia-fms device sim` is a device in a terminal.
- **Operators.** Match Play lists device faults with a button to clear them. Outputs can be tested by hand from
  the Devices page unless something else drives them.

## How to try it

```bash
go build
```

```bash
./galaxia-fms -dev
```

1. Setup → Settings: pick `ball-count`.
2. Setup → Devices → Virtual devices: click Beam-break counter, then Create, then Approve.
3. Setup → Bindings: Add input binding. The card reads "add the count to Balls for Red". Save.
4. Setup → Devices → Virtual devices: LED strip (30 px), Create, Approve.
5. Setup → Lighting: Add fixture, pick the strip, Save.
6. Run → Match Play: bypass every station, Start Match. Click +1 on the virtual counter and watch the score; watch
   the strip on the Lighting page follow the match.

## Verification

- **Go tests.** `go test ./...` passes in all 18 packages. The device tests include:
  - a device reconnecting 40 times in a row;
  - every transport's authentication, takeover and access changes;
  - the binding validate → YAML → validate round trip;
  - a virtual strip followed through a whole ball-count match.
- **The plan's three acceptance checks**, in a browser on a second copy with its own port and database:
  1. *A virtual beam-break bound in the browser scores in a live match.* A +1 in autonomous made red's score 2
     on Match Play.
  2. *A virtual LED strip configured only on the Lighting page follows the match through its scenes.* The page
     followed the whole match:
     - teams ready;
     - autonomous: the virtual device received a red and a blue segment in the game's colours;
     - teleop;
     - the endgame cue for 2 s;
     - match over;
     - score posted for 10 s;
     - idle.
  3. *Pulling a virtual E-stop's heartbeat faults the field.* With the E-stop box bound to station R1, switching
     its heartbeat off in autonomous E-stopped R1. Within 2.5 s Match Play listed "no heartbeat for 1.0
     seconds", and the lighting went to its E-stop scene. With the heartbeat back, "Clear device faults" cleared
     it.
- Each of the four commits that finished this milestone was built, vetted and tested on its own.

## Known limits

- **Hardware.** No real board has connected to the FMS's broker yet. A real serial port has not been opened
  either: the serial tests use in-memory port pairs.
- **The race detector** needs cgo, which this laptop lacks, so the threading was reasoned about rather than
  checked. CI runs on Linux, where it could be added.
- **Lighting.**
  - Theme colours are not offered yet; they come with milestone 9.
  - With `reverse` set, chase and rainbow still run in the device's own direction.
  - The countdown moments arrive with the countdown.
- **Field profiles.** Profiles can be made and chosen on the Settings page. They cannot be renamed or deleted
  from a page yet; the code supports both.
- **Virtual devices** live in memory, so they are gone after the FMS restarts. Their approvals and bindings
  stay.
