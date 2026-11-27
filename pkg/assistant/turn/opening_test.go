package turn

import (
	"testing"
	"time"
)

func TestBeforeDetectionCountsOnlyTheAudioAheadOfTheWake(t *testing.T) {
	pre := make([]int16, voiceRate*18/10)
	want := uint32(2 * voiceRate * 15 / 10)
	if got := beforeDetection(pre, 300*time.Millisecond); got != want {
		t.Fatalf("bytes = %d, want %d", got, want)
	}
	if got := beforeDetection(pre, 0); got != uint32(2*len(pre)) {
		t.Fatalf("bytes with no delay = %d, want all %d", got, 2*len(pre))
	}
	if got := beforeDetection(pre[:voiceRate/10], time.Second); got != 0 {
		t.Fatalf("bytes when the history is shorter than the delay = %d, want 0", got)
	}
}
