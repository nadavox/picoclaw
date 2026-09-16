package localvoice

import (
	"encoding/binary"
	"testing"
)

func TestIsWakeOnly(t *testing.T) {
	cases := []struct {
		frames int
		text   string
		want   bool
	}{
		{5, "HE GIBBAS", true},
		{5, "create a folder called test", false}, // late wake, full command
		{60, "hi", false},                         // real short command
	}
	for _, c := range cases {
		if got := isWakeOnly(c.frames, c.text); got != c.want {
			t.Errorf("isWakeOnly(%d, %q) = %v", c.frames, c.text, got)
		}
	}
}

func TestVADOpensOnSpeechAndClosesOnSilence(t *testing.T) {
	frame := func(amp int16) []byte {
		b := make([]byte, frameBytes)
		for i := 0; i < frameSamples; i++ {
			s := amp
			if i%2 == 1 {
				s = -amp
			}
			binary.LittleEndian.PutUint16(b[i*2:], uint16(s))
		}
		return b
	}
	v := newVAD()
	for range 20 {
		if v.push(frame(100)) {
			t.Fatal("quiet room opened the gate")
		}
	}
	if !v.push(frame(5000)) {
		t.Fatal("speech did not open the gate")
	}
	for range 8 {
		v.push(frame(100))
	}
	if v.active {
		t.Fatal("gate stayed open after 640 ms of silence")
	}
}

func TestSpeakable(t *testing.T) {
	if got := speakable("**Done.** Created `test_dir`"); got != "Done. Created test dir" {
		t.Errorf("got %q", got)
	}
}
