package turn

import (
	"bytes"
	"testing"
)

func TestTheGateHoldsAudioUntilReleasedThenPassesItInOrder(t *testing.T) {
	var sent [][]byte
	g := &gate{send: func(b []byte) { sent = append(sent, append([]byte(nil), b...)) }}

	first := bytes.Repeat([]byte{1}, releaseChunk)
	second := bytes.Repeat([]byte{2}, releaseChunk/2)
	g.pass(first)
	g.pass(second)
	if len(sent) != 0 || g.opened() {
		t.Fatalf("sent %d frames before release", len(sent))
	}

	g.release()
	g.pass([]byte{3})

	want := [][]byte{first, second, {3}}
	if len(sent) != len(want) {
		t.Fatalf("sent %d frames, want %d", len(sent), len(want))
	}
	for i := range want {
		if !bytes.Equal(sent[i], want[i]) {
			t.Errorf("frame %d is %d bytes of %v, want %d bytes of %v",
				i, len(sent[i]), sent[i][0], len(want[i]), want[i][0])
		}
	}
	if !g.opened() {
		t.Error("the gate is still shut after release")
	}
}
