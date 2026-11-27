package avatar

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

type spoke struct {
	handled bool
	rest    []int16
	err     error
}

// clipSeconds is how long the fed rig's clip runs.
const clipSeconds = 2

// fedRig is a connected avatar that Home Assistant feeds, on test timings in real time. Its clip
// is clipSeconds of 16 kHz WAV.
func fedRig(t *testing.T) *rig {
	t.Helper()
	cfg := always()
	cfg.Fed = true
	r := newRig(t, cfg)
	r.connect(t)
	realTime(r)
	r.lt.set(func(lt *liveTalking) { lt.clip = wav(clipSeconds * handbackRate) })
	return r
}

// wav is n samples of 16 kHz mono 16-bit WAV.
func wav(n int) []byte {
	data := make([]byte, 2*n)
	out := []byte("RIFF")
	out = binary.LittleEndian.AppendUint32(out, uint32(36+len(data)))
	out = append(out, "WAVEfmt "...)
	out = binary.LittleEndian.AppendUint32(out, 16)
	out = binary.LittleEndian.AppendUint16(out, 1)
	out = binary.LittleEndian.AppendUint16(out, 1)
	out = binary.LittleEndian.AppendUint32(out, handbackRate)
	out = binary.LittleEndian.AppendUint32(out, 2*handbackRate)
	out = binary.LittleEndian.AppendUint16(out, 2)
	out = binary.LittleEndian.AppendUint16(out, 16)
	out = append(out, "data"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(data)))
	return append(out, data...)
}

// left is how many samples of the rig's clip remain from from on.
func left(from time.Duration) int {
	return clipSeconds*handbackRate - int(from*handbackRate/time.Second)
}

func speakAsync(r *rig, ctx context.Context, sink Sink) <-chan spoke {
	out := make(chan spoke, 1)
	go func() {
		handled, rest, err := r.a.Speak(ctx, 0, r.url+"/clip.mp3", sink, nil)
		out <- spoke{handled, rest, err}
	}()
	return out
}

func wait(t *testing.T, c <-chan spoke) spoke {
	t.Helper()
	select {
	case s := <-c:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("Speak never returned")
		return spoke{}
	}
}

// uploaded waits for the clip to be sent to LiveTalking, failing if Speak returns first.
func uploaded(t *testing.T, r *rig, got <-chan spoke) {
	t.Helper()
	select {
	case <-r.uploaded:
	case s := <-got:
		t.Fatalf("returned (handled %v) without sending the clip", s.handled)
	case <-time.After(5 * time.Second):
		t.Fatal("the clip was never sent")
	}
}

// talk plays n chunks of 20 ms on the session, about as fast as they would arrive, once the reply
// listens to it.
func talk(r *rig, n int) {
	l := r.a.links[0]
	for give := time.Now().Add(2 * time.Second); time.Now().Before(give); {
		l.mu.Lock()
		listening := l.speech != nil
		l.mu.Unlock()
		if listening {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for range n {
		r.peer().play(chunk(4000))
		time.Sleep(time.Millisecond)
	}
}

func TestAFedReplyIsFollowedWithoutUploadingTheClip(t *testing.T) {
	r := fedRig(t)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "r1", true })
	sink := &lane{}

	got := speakAsync(r, context.Background(), sink)
	talk(r, 20)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "", false })
	s := wait(t, got)

	if !s.handled || s.rest != nil || s.err != nil {
		t.Fatalf("handled %v, %d samples left, err %v; want the avatar to have spoken it",
			s.handled, len(s.rest), s.err)
	}
	if len(r.lt.spoken) != 0 {
		t.Error("uploaded the clip though Home Assistant feeds the session")
	}
	if sink.voice == 0 {
		t.Error("none of the avatar's voice reached the speaker")
	}
}

// ha-vam feeds no cached reply, announcement or chat line: with no streamed reply open the clip
// goes to the avatar as it did before, without waiting for a stream.
func TestAFedSlotWithNoOpenReplySendsTheClipAsBefore(t *testing.T) {
	r := fedRig(t)
	r.a.timing.fedFirstSound = time.Minute
	r.lt.set(func(lt *liveTalking) { lt.clip = nil })

	got := speakAsync(r, context.Background(), &lane{})
	uploaded(t, r, got)
	wait(t, got)

	if len(r.lt.spoken) != 1 || string(r.lt.spoken[0]) != "ID3 clip" {
		t.Fatalf("uploaded %d clips, want the clip sent as before", len(r.lt.spoken))
	}
}

// The reply still named after it was followed is the last one, not this one.
func TestAReplyAlreadyFollowedIsNotFollowedAgain(t *testing.T) {
	r := fedRig(t)
	r.lt.set(func(lt *liveTalking) { lt.reply = "r1" })
	r.a.links[0].followed = "r1"

	got := speakAsync(r, context.Background(), &lane{})
	uploaded(t, r, got)
	wait(t, got)

	if len(r.lt.spoken) != 1 {
		t.Fatal("followed a reply that was already followed instead of sending the clip")
	}
}

// ha-vam opens the stream before Home Assistant has the clip's first bytes, and not always before
// the device asks; deciding earlier would speak a fed reply twice.
func TestTheDecisionWaitsForTheBackupsFirstBytes(t *testing.T) {
	r := fedRig(t)
	gate := make(chan struct{})
	r.lt.set(func(lt *liveTalking) { lt.gate = gate })

	got := speakAsync(r, context.Background(), &lane{})
	time.Sleep(20 * r.a.timing.poll)
	select {
	case s := <-got:
		t.Fatalf("decided (handled %v) before the clip's first bytes", s.handled)
	default:
	}
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "r1", true })
	close(gate)
	talk(r, 10)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "", false })

	if s := wait(t, got); !s.handled {
		t.Fatal("did not follow the reply opened before the first bytes")
	}
}

func TestAFedReplyWithNoSoundHandsBackFromTheStart(t *testing.T) {
	r := fedRig(t)
	r.lt.set(func(lt *liveTalking) { lt.reply = "r1" })

	s := wait(t, speakAsync(r, context.Background(), &lane{}))

	if s.handled || len(s.rest) != left(0) {
		t.Fatalf("handled %v, %d samples left; want all %d handed back",
			s.handled, len(s.rest), left(0))
	}
	if r.lt.interrupted != 1 {
		t.Errorf("interrupted %d times, want the silent reply stopped", r.lt.interrupted)
	}
}

// A dropout mid-reply hands back what is left, from a little before where the avatar stopped, so
// no syllable is lost and the start is not said twice.
func TestAFedReplyThatGoesSilentResumesFromWhereItStopped(t *testing.T) {
	r := fedRig(t)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "r1", true })
	sink := &lane{}

	got := speakAsync(r, context.Background(), sink)
	talk(r, 50) // 1 s of speech
	s := wait(t, got)

	from := time.Second - defaults.rewind
	if s.handled || len(s.rest) != left(from) {
		t.Fatalf("handled %v, %d samples left; want the clip from %s on (%d)",
			s.handled, len(s.rest), from, left(from))
	}
	if r.lt.interrupted != 1 || !sink.drained {
		t.Errorf("interrupted %d, drained %v", r.lt.interrupted, sink.drained)
	}
}

func TestAFedReplyResumesWhenLiveTalkingStopsAnswering(t *testing.T) {
	r := fedRig(t)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "r1", true })
	r.a.timing.fedSilent = time.Minute

	got := speakAsync(r, context.Background(), &lane{})
	talk(r, 30) // 0.6 s, past the rewind
	r.lt.set(func(lt *liveTalking) { lt.failing = 1000 })
	s := wait(t, got)

	if s.handled || len(s.rest) == 0 || len(s.rest) >= left(0) {
		t.Fatalf("handled %v, %d samples left; want handed back mid-reply", s.handled, len(s.rest))
	}
}

// One failed status call is a hiccup on the network, not a dropout.
func TestOneFailedStatusCallDoesNotEndAFedReply(t *testing.T) {
	r := fedRig(t)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "r1", true })

	got := speakAsync(r, context.Background(), &lane{})
	talk(r, 5)
	r.lt.set(func(lt *liveTalking) { lt.failing = 1 })
	talk(r, 10)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "", false })

	if s := wait(t, got); !s.handled {
		t.Fatalf("handed back (%d samples) after a single failed status call", len(s.rest))
	}
}

func TestStoppingAFedReplyInterruptsTheAvatar(t *testing.T) {
	r := fedRig(t)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "r1", true })
	ctx, cancel := context.WithCancel(context.Background())
	sink := &lane{}

	got := speakAsync(r, ctx, sink)
	talk(r, 5)
	cancel()
	s := wait(t, got)

	if !s.handled || s.err != nil {
		t.Fatalf("handled %v err %v, want nothing more spoken", s.handled, s.err)
	}
	if r.lt.interrupted != 1 || !sink.drained {
		t.Errorf("interrupted %d, drained %v", r.lt.interrupted, sink.drained)
	}
}

func TestAnAvatarNotFedStillUploadsTheClip(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	realTime(r)
	r.lt.set(func(lt *liveTalking) { lt.reply = "r1" })

	got := speakAsync(r, context.Background(), &lane{})
	uploaded(t, r, got)
	wait(t, got)

	if len(r.lt.spoken) != 1 {
		t.Fatalf("uploaded %d clips, want the clip sent as before", len(r.lt.spoken))
	}
}

// A reply paused at its source, as Home Assistant does while a tool runs, keeps the avatar: its
// silence is not a dropout, and the reply goes on once the speech does.
func TestAFedReplyHoldingThroughAPauseKeepsTheAvatar(t *testing.T) {
	r := fedRig(t)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "r1", true })
	got := speakAsync(r, context.Background(), &lane{})
	talk(r, 10)

	r.lt.set(func(lt *liveTalking) { lt.speaking, lt.holding = false, true })
	time.Sleep(4 * r.a.timing.fedSilent)
	select {
	case s := <-got:
		t.Fatalf("ended (handled %v) while the reply was holding", s.handled)
	default:
	}
	r.lt.set(func(lt *liveTalking) { lt.speaking, lt.holding = true, false })
	talk(r, 10)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "", false })

	if s := wait(t, got); !s.handled || r.lt.interrupted != 0 {
		t.Fatalf("handled %v, interrupted %d; want the avatar to have spoken it all",
			s.handled, r.lt.interrupted)
	}
}

// LiveTalking can report the hold while it still says it is speaking, as while it renders the
// last of the audio before the pause. That is still a hold, not silence.
func TestRegressionAHoldReportedWhileStillSpeakingKeepsTheAvatar(t *testing.T) {
	r := fedRig(t)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "r1", true })
	got := speakAsync(r, context.Background(), &lane{})
	talk(r, 10)

	r.lt.set(func(lt *liveTalking) { lt.holding = true })
	time.Sleep(4 * r.a.timing.fedSilent)

	select {
	case s := <-got:
		t.Fatalf("ended (handled %v) while the reply was holding", s.handled)
	default:
	}
	r.lt.set(func(lt *liveTalking) { lt.holding = false })
	talk(r, 10)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "", false })
	if s := wait(t, got); !s.handled || r.lt.interrupted != 0 {
		t.Fatalf("handled %v, interrupted %d; want the avatar to have spoken it all",
			s.handled, r.lt.interrupted)
	}
}

// The opener's quiet tail arrives before the hold does; together they outlast the silence limit
// counted from the last loud chunk, and the reply still keeps the avatar.
func TestRegressionAQuietTailThenAHoldKeepsTheAvatar(t *testing.T) {
	r := fedRig(t)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "r1", true })
	got := speakAsync(r, context.Background(), &lane{})
	talk(r, 10)
	for range 3 {
		r.peer().play(chunk(audible / 3))
		time.Sleep(10 * time.Millisecond)
	}

	r.lt.set(func(lt *liveTalking) { lt.holding = true })
	time.Sleep(4 * r.a.timing.fedSilent)

	select {
	case s := <-got:
		t.Fatalf("ended (handled %v) during the hold after the quiet tail", s.handled)
	default:
	}
	r.lt.set(func(lt *liveTalking) { lt.speaking, lt.holding = false, false })
	if s := wait(t, got); s.handled {
		t.Fatal("kept a reply that went silent after its hold")
	}
}

// A hold is the reply's only: another reply holding is no reason to wait.
func TestAnotherReplyHoldingIsNoHold(t *testing.T) {
	r := fedRig(t)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "r1", true })
	got := speakAsync(r, context.Background(), &lane{})
	talk(r, 10)

	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking, lt.holding = "r2", false, true })

	if s := wait(t, got); !s.handled {
		t.Fatal("did not end when the followed reply closed")
	}
}

// After a hold the silence counts from when it ended, not from the last sound before it.
func TestSilenceAfterAHoldCountsFromItsEnd(t *testing.T) {
	r := fedRig(t)
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking = "r1", true })
	got := speakAsync(r, context.Background(), &lane{})
	talk(r, 10)
	r.lt.set(func(lt *liveTalking) { lt.speaking, lt.holding = false, true })
	time.Sleep(4 * r.a.timing.fedSilent)

	ended := time.Now()
	r.lt.set(func(lt *liveTalking) { lt.holding = false })
	s := wait(t, got)

	if waited := time.Since(ended); waited < r.a.timing.fedSilent {
		t.Fatalf("broke off %s after the hold ended, want at least %s",
			waited, r.a.timing.fedSilent)
	}
	if s.handled {
		t.Fatal("kept a reply that went silent after its hold")
	}
}

// Home Assistant pauses the clip with the reply, longer than the device's client allows a whole
// fetch; a hand-back after it still has the rest.
func TestTheBackupClipOutlastsAPauseLongerThanTheClientTimeout(t *testing.T) {
	r := fedRig(t)
	r.a.o.Client.Timeout = 100 * time.Millisecond
	pause := make(chan struct{})
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking, lt.pause = "r1", true, pause })
	got := speakAsync(r, context.Background(), &lane{})
	talk(r, 50) // 1 s, then silence
	time.AfterFunc(300*time.Millisecond, func() { close(pause) })

	s := wait(t, got)

	from := time.Second - defaults.rewind
	if s.handled || len(s.rest) != left(from) {
		t.Fatalf("handled %v, %d samples left, err %v; want the clip from %s on (%d)",
			s.handled, len(s.rest), s.err, from, left(from))
	}
}

// A backup clip that stops arriving for good cannot be spoken; what the avatar said is not said
// again.
func TestABackupClipThatStopsArrivingLosesTheRest(t *testing.T) {
	r := fedRig(t)
	r.a.timing.backup = 200 * time.Millisecond
	pause := make(chan struct{})
	t.Cleanup(func() { close(pause) })
	r.lt.set(func(lt *liveTalking) { lt.reply, lt.speaking, lt.pause = "r1", true, pause })
	got := speakAsync(r, context.Background(), &lane{})
	talk(r, 50)

	s := wait(t, got)

	if !s.handled || s.rest != nil || !errors.Is(s.err, errIdle) {
		t.Fatalf("handled %v, %d samples left, err %v; want the rest lost to an idle clip",
			s.handled, len(s.rest), s.err)
	}
}
