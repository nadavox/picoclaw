# localvoice — a microphone and a speaker as a channel

> Fork-only channel (`github.com/nadavox/picoclaw`), built for the
> [edge](https://github.com/nadavox/edge) hand-held assistant on a Raspberry Pi
> Zero 2 W. Not in upstream sipeed/picoclaw.

`localvoice` turns a device's microphone and speaker into a PicoClaw channel.
Wake word and speech-to-text run **on the device** as sidecar processes; only the
agent's LLM calls leave it.

```
arecord ─► energy gate ─► wake sidecar   ("WAKE ...")
        └──────────────► stt sidecar    ("TURN <text>")  ─► agent ─► Send ─► flite ─► aplay
```

After a `WAKE`, the next `TURN` (within 15 s) becomes one inbound message.
Turns heard before the wake word, and anything heard while the device is
speaking, are dropped. A turn of three words or fewer that closes within 3.2 s
of the wake is treated as the wake phrase itself and ignored.

## Requirements

- `arecord`, `aplay` (alsa-utils) and `flite` on the device
- a wake sidecar: edge's Rust `ear` (`ear --stdin`)
- an STT sidecar: edge's `stt/sidecar.py` (sherpa-onnx) or `stt/rust`

Both sidecars read raw 16 kHz mono s16le on stdin. The protocols are defined
in edge's `voice/wake.go` and `voice/localstt.go`.

## Configuration

```json
{
  "channel_list": {
    "localvoice": {
      "enabled": true,
      "type": "localvoice",
      "settings": {
        "capture_device": "plughw:CARD=seeed2micvoicec,DEV=0",
        "playback_device": "plughw:CARD=seeed2micvoicec,DEV=0",
        "wake_cmd": ["/home/pi26/edge/ear/target/release/ear", "--stdin"],
        "stt_cmd": ["env", "MALLOC_TRIM_THRESHOLD_=0",
                    "/home/pi26/picoclaw/venv/bin/python", "/home/pi26/edge/stt/sidecar.py"],
        "voice": "slt"
      }
    }
  }
}
```

| Field | Required | Default | Notes |
|---|---|---|---|
| `wake_cmd` | yes | — | argv of the wake sidecar |
| `stt_cmd` | yes | — | argv of the STT sidecar; `MALLOC_TRIM_THRESHOLD_=0` saves ~9 MB idle |
| `capture_device` | no | `default` | on a Pi with the ReSpeaker HAT, `default` is HDMI and cannot record |
| `playback_device` | no | `default` | same |
| `voice` | no | `slt` | Flite voice |

Note the top-level key is `channel_list`, not `channels`.

## A config for a voice-only device

The on-device setup that was verified (Pi Zero 2 W, 2026-09-16) turns off
everything a small voice device does not need:

- `agents.defaults.restrict_to_workspace: true`, `max_tokens: 1024`, `max_tool_iterations: 6`
- `heartbeat.enabled: false`
- `tools`: `web`, `web_fetch`, `exec`, `cron`, `mcp`, `skills`, `install_skill`,
  `find_skills`, `spawn`, `spawn_status`, `subagent`, `load_image`, `send_file`,
  `send_tts` all `{"enabled": false}`, leaving the file tools and `make_dir`.
- `AGENTS.md` in the workspace asking for one or two short spoken sentences
  with no markdown.

Run it with `PICOCLAW_HOME=<dir> picoclaw gateway`.

## Related: the `make_dir` tool

Added in the same change: it creates a directory (and missing parents) under
`write_file`'s sandbox rules, so "make a folder" works with `exec` disabled. It
is enabled and disabled together with `write_file`.

## Verified

- `go test ./pkg/channels/localvoice ./pkg/tools/fs`
- On pi26 (Pi Zero 2 W), a recorded "Hey Jarvis. Make a folder called voice
  test." fed through a stand-in `arecord` created the folder through OpenRouter.
  Resident memory: picoclaw ~25–34 MB, ear ~2 MB, STT sidecar ~123–182 MB.
- Not yet verified with a live speaker in the room.

## Testing without a microphone

Put a script named `arecord` first in `PATH` that writes real-time 16 kHz mono
PCM (silence, a WAV, then silence) to stdout, and start the gateway. The
channel runs whatever `arecord` it finds.
