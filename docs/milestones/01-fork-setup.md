# Milestone 1: plan and fork setup

Finished 2026-11-12.

## What was done

- The plan was written, reviewed and approved: [docs/PLAN.md](../PLAN.md).
- `main` was reset to Team254's `main` (`248d9de`). The previous `main` is kept on the branch
  `legacy/community-2026`.
- The repository was renamed to `DavidMasin/galaxia-fms` and the Go module to
  `github.com/DavidMasin/galaxia-fms`. The binary is now `galaxia-fms`.
- `NOTICE.md` describes the relationship to Cheesy Arena. `LICENSE` is unchanged.
- The one behaviour carried over from the old `main`: a **radio only** network setting. With network security
  on and "Radio only" ticked, the access point is configured for each match's teams, the managed switch is not
  touched, and the wrong-station check is off. It is in Settings beside "Enable advanced network security".
- `.gitattributes` pins LF line endings, which `gofmt` requires.
- The GitHub Actions workflow was modernised: current action versions, the Go version taken from `go.mod`, a
  format check, `go vet`, build, test, and cross-compilation for linux/amd64, linux/arm64, windows/amd64 and
  darwin/arm64.

## How to try it

```bash
go build
```

```bash
./galaxia-fms -dev
```

Then open http://localhost:8080. The application is still upstream Cheesy Arena with its 2026 game at this
point.

## What is not done

**CI has not run.** The workflow is in place, but GitHub refuses to start any job on this account:
"The job was not started because your account is locked due to a billing issue." This needs fixing in GitHub's
billing settings. Until then nothing is verified on Linux.

**Local test results on the development laptop are partial.** Windows Smart App Control blocks some compiled
test binaries. On unmodified upstream code:

| Package | Result |
|---------|--------|
| `game`, `model`, `network`, `partner`, `playoff`, `plc` | pass |
| `field` | passes, except `TestPlcEStopAStop`, which fails intermittently on Windows |
| `web` | two failures on every run: `TestAllianceStationDisplayWebsocket`, `TestAnnouncerDisplayWebsocket` |
| `led`, `tournament`, `websocket` | not run: test binary blocked by Smart App Control |

The `web` and `field` failures are in upstream code, are tied to 2026 timing logic that milestone 3 removes,
and do not occur on upstream's own Linux CI. Their cause on Windows was not investigated further.

`go vet` runs with three analyzers switched off (`composites`, `hostport`, `structtag`) because unmodified
upstream code trips them. They will be switched back on as that code is reworked.
