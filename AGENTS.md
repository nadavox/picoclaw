# AGENTS.md — nadavox/picoclaw (fork)

This is a fork of [sipeed/picoclaw](https://github.com/sipeed/picoclaw) (MIT)
used as the agent for the [edge](https://github.com/nadavox/edge) hand-held
voice assistant. Upstream's own docs (`README.md`, `CONTRIBUTING.md`, `docs/`)
describe PicoClaw itself and stay as upstream wrote them.

`CLAUDE.md` is a symlink to this file.

## What the fork adds

| Path | What | Doc |
|---|---|---|
| `pkg/channels/localvoice/` | microphone + speaker channel; wake word and STT run as on-device sidecars from edge | [`docs/channels/localvoice/README.md`](docs/channels/localvoice/README.md) |
| `pkg/tools/fs` `make_dir` | sandboxed directory creation, so voice commands need no `exec` | same doc, § make_dir |
| `pkg/gateway/gateway.go` | one import line registering `localvoice` | — |

Everything else is upstream code. Keep the fork's diff this small.

## Build and test

```sh
go test ./pkg/channels/localvoice ./pkg/tools/fs
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags goolm,stdjson -ldflags "-s -w" \
  -o build/picoclaw-linux-arm64 ./cmd/picoclaw          # Pi Zero 2 W binary
go test -tags goolm,stdjson ./pkg/gateway/               # the gateway needs these tags
```

On macOS three upstream `pkg/tools` shell tests fail the same way on an
unmodified checkout (temp-dir paths); they are not fork regressions.

## Rules

- **Never open a PR to upstream `sipeed/picoclaw`** without a human decision:
  what gets open-sourced is an open partnership term.
- **Pulling upstream:** `git remote add upstream https://github.com/sipeed/picoclaw.git`,
  merge `upstream/main` into a branch, rerun the tests above, PR into `main`.
- **Sidecar protocols belong to edge** (`voice/wake.go`, `voice/localstt.go`).
  Change them there first.
- **Never commit a config with keys.** The device config lives outside the
  repo (on pi26: `~/picoclaw/home/config.json`, mode 600).
- The device facts (audio device name, memory, Wi-Fi, shared-board etiquette)
  are in edge's `AGENTS.md` § The board.
