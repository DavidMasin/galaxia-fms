# Milestone 4: events, API, logins and webhooks

Finished 2026-11-21. It came after milestones 5 and 6, at David's request (see [WORKLOG.md](../../WORKLOG.md)).

## What works

- **A read API** under `/api/v1`, described in [docs/openapi.yaml](../openapi.yaml):
  - the clock and health;
  - the active game, the match on the field with its clock and live score;
  - matches with results, and one match with its full audit log;
  - rankings, teams and alliances;
  - exports of matches, scores, rankings and the audit log as CSV or JSON;
  - robot connection reports;
  - game definitions, which an admin can also save.
- **Events three ways.** Everything that happens on the field is an event:
  - polled from `/api/v1/events`;
  - streamed from `/api/v1/events/stream` (Server-Sent Events, resuming after a reconnect) or `/api/v1/events/ws`;
  - sent to your own program by **webhooks**.
- **Webhooks.** Setup → Webhooks adds a webhook, chooses its events, sends a test event and shows each
  webhook's delivery log with Resend.
  - Deliveries are signed with HMAC-SHA256.
  - A failed delivery is retried with growing waits for 24 hours.
  - Deliveries are kept in the database, so they survive a restart.

  [docs/webhooks.md](../webhooks.md) is the guide for receivers.
- **The journal.** Every event is also appended to a file per day in a `journal` folder beside the database, and
  can be downloaded from `/api/v1/journal/{day}`. It outlives a restart; 30 days are kept.
- **Logins and roles.** Logins are off until the settings have an admin password or a user exists. Then:

  | Role | Can |
  |------|-----|
  | admin | Everything |
  | scorer | Scoring panels, and match review with its edits |
  | referee | The referee panel |
  | read-only | Every API read |

  Displays, rankings, reports, match logs and the match review list stay open, so a TV or a team's phone needs no
  login.
- **API tokens.** Setup → Users and API Tokens makes a token with a role for a program to send as
  `Authorization: Bearer <token>`. The FMS keeps only a hash, and shows the token once.

## How to try it

```bash
go build
```

```bash
./galaxia-fms -dev
```

1. Open http://localhost:8080/api/v1/match and http://localhost:8080/api/v1/events in a browser.
2. Setup → Webhooks: Add webhook, with the address of a program of yours. The two short receivers in
   [docs/webhooks.md](../webhooks.md) work as they are. Press "Send a test event", then play a match and watch
   the Deliveries page.
3. Setup → Users and API Tokens: add a user with the admin role. Logins are now on, and you are logged in as that
   user. Add a scorer, log out, and log in as the scorer: the scoring panels open and Settings does not.
4. Make a read-only token and try it:

```bash
curl -H "Authorization: Bearer <token>" http://localhost:8080/api/v1/rankings
```

## Verification

The plan's two checks for this milestone:

1. *The OpenAPI conformance test passes.* Two tests keep the description and the server in step:
   - every `/api/v1` route the server registers must be described, and nothing may be described that does not
     exist;
   - about 80 real requests are sent, and each answer, errors included, is validated against the schema the
     description gives for its status.

   I checked that the second can fail: with a required field added to one schema and a type changed in another,
   it reported both.
2. *A webhook receiver in the tests verifies signatures and retry.* A test receiver recomputes each signature as
   a third party would, fails on purpose, and the tests check the retries, the growing waits, the final status
   and that the delivery id stays the same. Other tests cover:
   - the 24-hour limit;
   - an outbox that survives a restart;
   - pruning and the limit on waiting deliveries;
   - resend, new secrets, filters and disabled webhooks.

Also:

- `go test ./...` passes in all 19 packages; formatting and vet are clean.
- **CI** passed on Linux for these commits: formatting, vet, build, every test, the four cross-compiles, and
  the SDK job.
- **In a browser**, on a second copy with its own port and database:
  - The first admin user turned logins on. A scorer reached the scoring panel after logging in and got "Not
    allowed" on Settings.
  - A read-only token read the API, and was refused when it tried to change a game or list webhooks.
  - Two webhooks delivered a test event and a whole match to a Python and a Node receiver running the code
    printed in the guide. Both receivers refused a forged and an unsigned delivery.
  - With one receiver stopped, the log showed the delivery retrying. After it was restarted, Resend delivered it
    on the third attempt.
  - The journal's file for the day held the run's events and downloaded through the API.

## Known limits

- **The API reads; it does not run a match.** Starting, aborting and committing matches, and managing devices,
  bindings and lighting, are done on the pages. The API changes only games and webhooks.
- **Committing a match stays with the admin** on Match Play. The plan listed it under scorer; splitting Match Play
  by role is a larger change.
- **Read-only** means the API. There are no read-only views of the setup pages.
- **Events not published yet:** `schedule.changed`, `alliance_selection.updated`, `alliance_selection.finalized`,
  `rankings.updated` and `score.totals`. A webhook can already subscribe to them.
- **The event stream replays one run only.** After a restart, sequence numbers begin again (a `system.started`
  event marks it), and earlier events are in the journal, not in the stream.
- **Carried over from Cheesy Arena, unchanged:**
  - the admin password in the settings is stored as it is typed, while users' passwords are hashed;
  - login sessions do not expire by themselves;
  - pages are rendered without automatic HTML escaping, so the new pages escape what people type by hand.
- **Displays have no token option yet.** They are always open.
- **Data races.** A CI job now runs the race detector on Linux, which this laptop cannot. Everything built
  for this milestone and for milestones 5 and 6 is clean. The arena, the web handlers and two smaller
  packages inherited from Cheesy Arena were not. **Fixed on 2026-11-21**: every package is clean and CI requires
  it; see [architecture.md](../architecture.md#the-arenas-lock).
