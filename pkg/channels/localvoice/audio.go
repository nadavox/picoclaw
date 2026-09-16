package localvoice

import (
	"bufio"
	"encoding/binary"
	"io"
	"math"
	"os/exec"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// Audio is fixed end to end: 16 kHz, mono, signed 16-bit little-endian. It is
// what the wake model and the recogniser are trained on, so nothing resamples.
// Ported from github.com/nadavox/edge voice/audio.go and voice/wake.go.
const (
	sampleRate   = 16000
	frameSamples = 1280 // 80 ms
	frameBytes   = frameSamples * 2
)

// sidecar is a child process fed raw PCM on stdin that reports events as
// lines on stdout. Both the wake engine (`ear --stdin` -> "WAKE ...") and the
// recogniser (`sidecar.py` -> "TURN <text>") speak this protocol, so the
// engines can change without touching Go and without cgo.
type sidecar struct {
	cmd   *exec.Cmd
	in    chan []byte
	lines chan string
}

// startSidecar keeps only stdout lines starting with prefix, prefix removed.
func startSidecar(argv []string, prefix string) (*sidecar, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &sidecar{cmd: cmd, in: make(chan []byte, 32), lines: make(chan string, 4)}

	go func() {
		defer stdin.Close()
		for b := range s.in {
			if _, err := stdin.Write(b); err != nil {
				return
			}
		}
	}()
	go func() {
		defer close(s.lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			if rest, ok := strings.CutPrefix(sc.Text(), prefix); ok {
				select {
				case s.lines <- strings.TrimSpace(rest):
				default: // an event nobody collected is stale by definition
				}
			}
		}
	}()
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			logger.DebugCF("localvoice", "sidecar", map[string]any{"cmd": argv[0], "line": sc.Text()})
		}
	}()
	return s, nil
}

// feed drops rather than blocks: a slow engine must not make live audio lag.
func (s *sidecar) feed(b []byte) {
	select {
	case s.in <- b:
	default:
	}
}

func (s *sidecar) stop() {
	close(s.in)
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.cmd.Wait()
}

// mic runs arecord and emits fixed 80 ms frames until it dies.
//
// ponytail: shells out to arecord instead of binding ALSA; keeps the binary
// cgo-free so it still cross-compiles with plain GOOS/GOARCH.
func mic(device string) (<-chan []byte, *exec.Cmd, error) {
	cmd := exec.Command("arecord", "-q", "-D", device,
		"-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "raw")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	frames := make(chan []byte, 32)
	go func() {
		defer close(frames)
		for {
			b := make([]byte, frameBytes)
			if _, err := io.ReadFull(out, b); err != nil {
				return
			}
			select {
			case frames <- b:
			default: // stalled consumer: drop, never fall behind wall-clock
			}
		}
	}()
	return frames, cmd, nil
}

// vad is an energy gate with an adaptive noise floor. It only decides whether
// the wake model runs at all; running it on silence pegs a Zero 2 W core.
//
// ponytail: RMS + adaptive floor, not Silero. Upgrade if fans or music cause
// false wakes; the interface is one bool.
type vad struct {
	floor  float64
	quiet  int
	active bool
}

func newVAD() *vad { return &vad{floor: 300} }

func (v *vad) push(b []byte) bool {
	var sum float64
	for i := 0; i+1 < len(b); i += 2 {
		s := float64(int16(binary.LittleEndian.Uint16(b[i:])))
		sum += s * s
	}
	rms := math.Sqrt(sum / float64(len(b)/2))

	if rms > v.floor*3 {
		v.active, v.quiet = true, 0
	} else if v.active {
		v.quiet++
		if v.quiet >= 8 { // 640 ms survives a mid-sentence breath
			v.active = false
		}
	}
	// Track the floor only while idle, or a long sentence raises it to itself.
	if !v.active {
		v.floor = 0.95*v.floor + 0.05*rms
		if v.floor < 50 {
			v.floor = 50
		}
	}
	return v.active
}
