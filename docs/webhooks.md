# Webhooks

A webhook sends what happens on the field to a program of yours, as one HTTP POST per event: a stream overlay, a
scoreboard, a chat bot. This page is for the person writing that program. Webhooks are set up on **Setup →
Webhooks**, or through `/api/v1/webhooks` ([openapi.yaml](openapi.yaml)).

If your program would rather ask than be told, use the event stream instead: `/api/v1/events`,
`/api/v1/events/stream` or `/api/v1/events/ws`. The events are the same.

## What arrives

A POST to the address you gave, with a JSON body and these headers:

| Header | Value |
|--------|-------|
| `Content-Type` | `application/json` |
| `X-Galaxia-Event` | The event type, such as `match.started` |
| `X-Galaxia-Delivery` | The id of this delivery. A retry or a resend of the same delivery carries the same id. |
| `X-Galaxia-Timestamp` | When this attempt was sent, in Unix seconds |
| `X-Galaxia-Signature` | `sha256=` and the signature, described below |
| `X-Galaxia-Attempt` | 1 for the first try, 2 for the first retry, and so on |
| `User-Agent` | `Galaxia-FMS-Webhooks/1` |

The body is the event, in the same envelope the event stream uses:

```json
{
  "seq": 1042,
  "time": "2026-11-21T19:26:52.2257712Z",
  "type": "match.phase_changed",
  "match": "Q12",
  "data": {"duration": 15, "index": 0, "phase": "auto", "robots": "auto"}
}
```

- `seq` grows by one with every event the FMS publishes, so a gap means your webhook was not subscribed to the
  events in between. It starts again from 1 when the FMS restarts.
- `match` is the short name of the match on the field, when there is one.
- `data` depends on the event type.

Answer with any 2xx status within 10 seconds. Do slow work after answering. Redirects are not followed.

## Checking the signature

Anyone on the network can POST to your program. The signature proves a delivery came from the FMS that holds your
webhook's secret:

1. Take the `X-Galaxia-Timestamp` header, a full stop, and the raw body, exactly as received.
2. Compute HMAC-SHA256 of that with the secret as the key, written as lowercase hex.
3. Compare `sha256=` plus that with `X-Galaxia-Signature`, using a constant-time comparison.
4. Refuse a timestamp more than a few minutes from your own clock, so that a captured delivery cannot be replayed
   later. Keep both clocks set; on a field without internet, the FMS's `/api/v1/time` says what it thinks the time
   is.

The secret is shown on the Webhooks page when a webhook is added, and under Edit afterwards. It is used as text,
as it is written. "New secret" replaces it at once, including for deliveries already waiting.

Both programs below were run against the FMS on 2026-11-21. Each accepted a test event and the events of a match,
and refused a delivery with a wrong signature and one with none.

**Python** (standard library only):

```python
"""Receives Galaxia FMS webhooks: checks each delivery's signature, then prints the event."""

import hashlib
import hmac
import json
import os
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

SECRET = os.environ["GALAXIA_WEBHOOK_SECRET"].encode()  # From the Webhooks page.
PORT = int(os.environ.get("PORT", "9000"))
MAX_AGE_SECONDS = 300


def is_genuine(headers, body):
    """True if the delivery was signed with our secret, recently."""
    timestamp = headers.get("X-Galaxia-Timestamp", "")
    if not timestamp.isdigit() or abs(time.time() - int(timestamp)) > MAX_AGE_SECONDS:
        return False
    expected = "sha256=" + hmac.new(SECRET, timestamp.encode() + b"." + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, headers.get("X-Galaxia-Signature", ""))


class Handler(BaseHTTPRequestHandler):
    seen = set()  # Delivery ids already handled; a retry or a resend repeats one.

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        if not is_genuine(self.headers, body):
            self.send_response(401)
            self.end_headers()
            return
        delivery = self.headers["X-Galaxia-Delivery"]
        if delivery not in self.seen:
            self.seen.add(delivery)
            event = json.loads(body)
            print(event["type"], event.get("match", ""), event.get("data"), flush=True)
        # Answer quickly with any 2xx status; anything else is retried.
        self.send_response(204)
        self.end_headers()


HTTPServer(("0.0.0.0", PORT), Handler).serve_forever()
```

**JavaScript** (Node.js, standard library only):

```js
// Receives Galaxia FMS webhooks: checks each delivery's signature, then prints the event.

const crypto = require("crypto");
const http = require("http");

const SECRET = process.env.GALAXIA_WEBHOOK_SECRET; // From the Webhooks page.
const PORT = Number(process.env.PORT || 9000);
const MAX_AGE_SECONDS = 300;

// True if the delivery was signed with our secret, recently.
function isGenuine(headers, body) {
  const timestamp = headers["x-galaxia-timestamp"] || "";
  if (!/^\d+$/.test(timestamp) || Math.abs(Date.now() / 1000 - Number(timestamp)) > MAX_AGE_SECONDS) {
    return false;
  }
  const expected = "sha256=" +
    crypto.createHmac("sha256", SECRET).update(timestamp + ".").update(body).digest("hex");
  const given = String(headers["x-galaxia-signature"] || "");
  return given.length === expected.length && crypto.timingSafeEqual(Buffer.from(given), Buffer.from(expected));
}

const seen = new Set(); // Delivery ids already handled; a retry or a resend repeats one.

http.createServer((request, response) => {
  const chunks = [];
  request.on("data", (chunk) => chunks.push(chunk));
  request.on("end", () => {
    const body = Buffer.concat(chunks);
    if (request.method !== "POST" || !isGenuine(request.headers, body)) {
      response.writeHead(401).end();
      return;
    }
    const delivery = request.headers["x-galaxia-delivery"];
    if (!seen.has(delivery)) {
      seen.add(delivery);
      const event = JSON.parse(body);
      console.log(event.type, event.match || "", JSON.stringify(event.data));
    }
    // Answer quickly with any 2xx status; anything else is retried.
    response.writeHead(204).end();
  });
}).listen(PORT);
```

A check value for your own code: with the secret `key`, the timestamp `1` and the body `body`, the header is
`sha256=91b5374b153842ad05b2c4eab9349b8321b14703165bd3fb8b034dfb8be98ae5`.

## Retries, and handling a delivery twice

A delivery fails if your program answers with anything but a 2xx status, does not answer in 10 seconds, or cannot
be reached. A failed delivery is tried again:

- after about 10 seconds, then 20, 40 and so on, doubling up to an hour, each wait varied by up to a fifth;
- for 24 hours from when it was queued, about 33 attempts in all;
- then it is marked failed and stays in the log, where **Resend** sends it again for another 24 hours.

So that nothing is lost and nothing is handled twice:

- **Remember the `X-Galaxia-Delivery` ids you have handled**, as the examples do. The same delivery comes again
  when your answer was lost on the way back, and when someone presses Resend.
- **Order.** One webhook is sent one delivery at a time, oldest first, so events arrive in order while your
  program is healthy. After a failure, later events can overtake the one being retried. Use `seq` and `time` if
  order matters.
- **Outages.** The outbox is kept in the FMS's database, so deliveries waiting when the FMS stops are sent when
  it starts again.
- **Disabled webhooks.** No new events are queued for a disabled webhook, so it misses what happens meanwhile.
  Deliveries that were already waiting are held, and sent if it is enabled again within their 24 hours. Tests and
  resends are sent even while it is disabled.
- **Limits.** At most 5,000 deliveries wait for one webhook; past that, the oldest waiting one is given up. The
  log keeps the latest 500 finished deliveries per webhook.

## Choosing events

A webhook lists event types or prefixes. A prefix selects everything beneath it: `match` selects `match.started`
and `match.committed`. `*` selects every event. A new webhook starts with the match from loading to its committed
result.

| Type | When | Chosen by default |
|------|------|-------------------|
| `match.loaded` | A match was loaded onto the field, with its teams | yes |
| `match.started` | The match started | yes |
| `match.phase_changed` | A new phase of the match began | yes |
| `match.cue` | A timed cue of the game fired | |
| `match.ended` | The match ran to its end | yes |
| `match.aborted` | The match was stopped early | yes |
| `match.committed` | The score was committed: the official result | yes |
| `match.result_edited` | A committed result was edited | yes |
| `score.changed` | One change to the live score. Many a second in a busy match. | |
| `score.undone` | A score change was undone | |
| `station.status` | A driver station, robot, radio, stop or bypass changed at a station | |
| `field.reset`, `field.volunteers` | The field was signalled safe | |
| `device.online`, `device.offline`, `device.pending`, `device.manifest` | A field device came, went, or described itself | |
| `device.fault`, `device.fault_cleared`, `device.refused` | Safety faults and refused inputs | |
| `device.input`, `device.output` | A device input or output changed. Many a second. | |
| `system.started` | The FMS started; `seq` begins again from here | |
| `system.game_changed`, `system.game_reloaded` | The game was changed or its file reloaded | |

The page also lists `schedule.changed`, `alliance_selection.updated`, `alliance_selection.finalized` and
`rankings.updated`. The FMS does not publish these yet; a webhook subscribed to them receives them once it does.

Two types are sent whatever a webhook's filters say:

- `webhook.test`: a test delivery, from the "Send a test event" button or the API.
- `webhook.gap`: events were lost before they could be queued, which can only happen if the FMS is overwhelmed.
  Its `data` says after which `seq` and before which. Reload what you keep from the API when you receive one.
