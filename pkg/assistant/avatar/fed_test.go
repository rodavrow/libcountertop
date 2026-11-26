package avatar

import (
	"context"
	"testing"
	"time"
)

type spoke struct {
	handled bool
	from    time.Duration
	err     error
}

// fedRig is a connected avatar that Home Assistant feeds, on test timings in real time.
func fedRig(t *testing.T) *rig {
	t.Helper()
	cfg := always()
	cfg.Fed = true
	r := newRig(t, cfg)
	r.connect(t)
	realTime(r)
	return r
}

func speakAsync(r *rig, ctx context.Context, sink Sink) <-chan spoke {
	out := make(chan spoke, 1)
	go func() {
		handled, from, err := r.a.Speak(ctx, 0, r.url+"/clip.mp3", sink, nil)
		out <- spoke{handled, from, err}
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

	if !s.handled || s.from != 0 || s.err != nil {
		t.Fatalf(
			"handled %v from %s err %v, want the avatar to have spoken it",
			s.handled,
			s.from,
			s.err,
		)
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

	if s.handled || s.from != 0 {
		t.Fatalf("handled %v from %s, want it handed back from the start", s.handled, s.from)
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

	want := time.Second - defaults.rewind
	if s.handled || s.from != want {
		t.Fatalf("handled %v from %s, want handed back from %s", s.handled, s.from, want)
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

	if s.handled || s.from == 0 {
		t.Fatalf("handled %v from %s, want handed back mid-reply", s.handled, s.from)
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
		t.Fatalf("handed back (from %s) after a single failed status call", s.from)
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
