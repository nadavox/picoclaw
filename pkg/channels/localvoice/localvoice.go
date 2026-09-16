// Package localvoice is an on-device voice channel: a microphone and a
// speaker instead of a chat app. Wake word and speech-to-text run locally as
// sidecar processes (see audio.go); only the agent's LLM calls leave the device.
//
//	arecord -> vad -> wake sidecar ("WAKE")
//	        \-> stt sidecar ("TURN <text>") -> agent -> Send -> flite -> aplay
package localvoice

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	ChannelType = "localvoice"
	chatID      = "localvoice:mic"

	// A turn that closes this soon after the wake word and says this little
	// is the wake phrase itself ("hey jarvis" -> "HE GIBBAS"), not a command.
	// edge/voice measured 5-9 frames in the simulator; on a Zero 2 W the
	// recogniser (RTF ~0.5) plus its 600 ms end-of-turn silence lands later,
	// so the window is a guessed 3.2 s. Tune from the "heard" log timings.
	// ponytail: a whole 1-3 word command inside that window ("hey jarvis,
	// stop") is dropped too; strip the wake phrase by text if that matters.
	wakeOnlyFrames = 40
	wakeOnlyWords  = 3
	// How long after the wake word a command may still start: 15 s.
	armedFrames = 15 * sampleRate / frameSamples
)

type Settings struct {
	CaptureDevice  string   `json:"capture_device,omitempty"`
	PlaybackDevice string   `json:"playback_device,omitempty"`
	WakeCmd        []string `json:"wake_cmd"`
	STTCmd         []string `json:"stt_cmd"`
	Voice          string   `json:"voice,omitempty"` // flite voice
}

func init() {
	config.RegisterChannelSettings(ChannelType, Settings{})
	channels.RegisterSafeFactory(ChannelType,
		func(bc *config.Channel, s *Settings, b *bus.MessageBus) (channels.Channel, error) {
			return New(bc, s, b)
		})
}

type Channel struct {
	*channels.BaseChannel
	s        *Settings
	speaking atomic.Bool
	speakMu  sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
}

func New(bc *config.Channel, s *Settings, b *bus.MessageBus) (*Channel, error) {
	if len(s.WakeCmd) == 0 || len(s.STTCmd) == 0 {
		return nil, fmt.Errorf("localvoice: wake_cmd and stt_cmd are required")
	}
	if s.CaptureDevice == "" {
		s.CaptureDevice = "default"
	}
	if s.PlaybackDevice == "" {
		s.PlaybackDevice = "default"
	}
	if s.Voice == "" {
		s.Voice = "slt"
	}
	base := channels.NewBaseChannel(ChannelType, s, b, bc.AllowFrom,
		channels.WithReasoningChannelID(bc.ReasoningChannelID))
	return &Channel{BaseChannel: base, s: s}, nil
}

func (c *Channel) Start(ctx context.Context) error {
	frames, arecord, err := mic(c.s.CaptureDevice)
	if err != nil {
		return fmt.Errorf("localvoice: mic: %w", err)
	}
	wake, err := startSidecar(c.s.WakeCmd, "WAKE")
	if err != nil {
		_ = arecord.Process.Kill()
		return fmt.Errorf("localvoice: wake: %w", err)
	}
	stt, err := startSidecar(c.s.STTCmd, "TURN ")
	if err != nil {
		wake.stop()
		_ = arecord.Process.Kill()
		return fmt.Errorf("localvoice: stt: %w", err)
	}

	ctx, c.cancel = context.WithCancel(ctx)
	c.done = make(chan struct{})
	go func() {
		defer close(c.done)
		defer wake.stop()
		defer stt.stop()
		defer func() { _ = arecord.Process.Kill(); _ = arecord.Wait() }()
		c.listen(ctx, frames, wake, stt)
	}()
	c.SetRunning(true)
	logger.InfoCF("localvoice", "listening", map[string]any{"device": c.s.CaptureDevice})
	return nil
}

// listen is the whole state machine: idle until WAKE, then the next TURN is
// the command. The recogniser hears every frame (it owns endpointing), so any
// turn finished before the wake word is room noise and is dropped.
func (c *Channel) listen(ctx context.Context, frames <-chan []byte, wake, stt *sidecar) {
	gate := newVAD()
	var n, wokeAt int
	armed := false
	for {
		var f []byte
		var ok bool
		select {
		case <-ctx.Done():
			return
		case f, ok = <-frames:
			if !ok {
				logger.ErrorCF("localvoice", "microphone stopped", nil)
				return
			}
		}
		n++
		if gate.push(f) {
			wake.feed(f)
		}
		stt.feed(f)

		select {
		case _, ok := <-wake.lines:
			if !ok {
				logger.ErrorCF("localvoice", "wake sidecar exited", nil)
				return
			}
			// ponytail: no barge-in; our own voice saying "Jarvis" must not re-wake.
			if !c.speaking.Load() {
				armed, wokeAt = true, n
				logger.InfoC("localvoice", "wake")
			}
		case text, ok := <-stt.lines:
			if !ok {
				logger.ErrorCF("localvoice", "stt sidecar exited", nil)
				return
			}
			if !armed || c.speaking.Load() {
				continue // pre-wake chatter or the tail of our own reply
			}
			if isWakeOnly(n-wokeAt, text) {
				continue // stay armed: the command may follow a pause
			}
			armed = false
			logger.InfoCF("localvoice", "heard", map[string]any{"text": text})
			c.HandleInboundContext(ctx, chatID, text, nil, bus.InboundContext{
				Channel: ChannelType, ChatID: chatID, ChatType: "direct", SenderID: "local",
			})
		default:
		}
		if armed && n-wokeAt > armedFrames {
			armed = false
		}
	}
}

func isWakeOnly(framesSinceWake int, text string) bool {
	return framesSinceWake < wakeOnlyFrames && len(strings.Fields(text)) <= wakeOnlyWords
}

func (c *Channel) Stop(context.Context) error {
	c.SetRunning(false)
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
	return nil
}

func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}
	text := speakable(msg.Content)
	if text == "" {
		return nil, nil
	}
	return nil, c.speak(ctx, text)
}

// speak renders with flite to a temp WAV and plays it on the chosen device.
//
// ponytail: whole reply synthesized at once, not sentence-streamed; add
// streaming if replies get long enough that the first-word delay is felt.
func (c *Channel) speak(ctx context.Context, text string) error {
	c.speakMu.Lock()
	defer c.speakMu.Unlock()
	c.speaking.Store(true)
	defer c.speaking.Store(false)

	wav, err := os.CreateTemp("", "localvoice-*.wav")
	if err != nil {
		return err
	}
	wav.Close()
	defer os.Remove(wav.Name())

	if out, err := exec.CommandContext(ctx, "flite", "-voice", c.s.Voice,
		"-t", text, "-o", wav.Name()).CombinedOutput(); err != nil {
		return fmt.Errorf("flite: %w: %s", err, out)
	}
	if out, err := exec.CommandContext(ctx, "aplay", "-q", "-D", c.s.PlaybackDevice,
		wav.Name()).CombinedOutput(); err != nil {
		return fmt.Errorf("aplay: %w: %s", err, out)
	}
	return nil
}

// speakable drops markdown that a synthesizer would read out as symbols.
func speakable(s string) string {
	return strings.TrimSpace(strings.NewReplacer("*", "", "#", "", "`", "", "_", " ").Replace(s))
}
