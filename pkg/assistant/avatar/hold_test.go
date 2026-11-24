package avatar

import (
	"context"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	display "github.com/ygelfand/libcountertop/pkg/display/geometry"
	"github.com/ygelfand/libcountertop/pkg/media/rtc"
)

// spokeOnScreen has slot 1's avatar show its picture and speak one reply, then puts the rig back
// on its own clock, from now.
func spokeOnScreen(t *testing.T, r *rig) {
	t.Helper()
	realTime(r)
	// The fake never says it is speaking; do not wait long for it to start.
	r.a.timing.firstSound = 60 * time.Millisecond
	p := r.peer()
	r.a.Showing(0)
	p.show(rtc.Frame{Key: true})
	if !r.a.Place(0, display.Rect{W: 10, H: 10}, false) {
		t.Fatal("no picture to speak over")
	}
	done := make(chan bool)
	go func() {
		handled, err := r.a.Speak(context.Background(), 0, r.url+"/clip.mp3", &lane{}, nil)
		if err != nil {
			t.Error(err)
		}
		done <- handled
	}()
	<-r.uploaded
	for range 5 {
		p.play(chunk(4000))
	}
	if !<-done {
		t.Fatal("the reply was not spoken by the avatar")
	}
	r.clock.at = time.Now()
	r.a.now = r.clock.now
}

func holdOn(r *rig) { r.a.hold.sw.OnCommand(true) }

func TestTheHoldSwitchIsOnTheMainDeviceAndStartsOff(t *testing.T) {
	r := newRig(t, always())

	ents := r.a.DeviceEntities()

	if len(ents) != 1 {
		t.Fatalf("%d device entities, want 1", len(ents))
	}
	b := baseOf(ents[0])
	if b.Name != "Avatar hold" || b.ObjectID != "avatar_hold" || b.DeviceID != 0 {
		t.Errorf("name %q, object id %q, device %d", b.Name, b.ObjectID, b.DeviceID)
	}
	if r.a.hold.sw.Get() {
		t.Error("the hold is on at boot")
	}
}

func TestAnAvatarThatSpokeStaysOnScreenWhileHeld(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	holdOn(r)

	spokeOnScreen(t, r)
	// Longer than an unplaced picture lasts, shorter than a stall.
	r.clock.pass(defaults.unplaced * 2)
	r.a.reconcile()

	if slot, ok := r.a.Held(); !ok || slot != 0 {
		t.Fatalf("held %d %v, want slot 1", slot, ok)
	}
	if r.scr.hidden {
		t.Error("the held picture was hidden while nothing placed it")
	}
}

func TestWithoutTheHoldTheAvatarIsNotKept(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)

	spokeOnScreen(t, r)
	r.clock.pass(defaults.unplaced * 5)
	r.a.reconcile()

	if _, ok := r.a.Held(); ok {
		t.Fatal("held with the switch off")
	}
	if !r.scr.hidden {
		t.Error("the picture stayed up with nothing placing it")
	}
}

func TestATouchLetsTheHeldAvatarGoAndKeepsTheSwitchOn(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	holdOn(r)
	spokeOnScreen(t, r)

	if !r.a.Release() {
		t.Fatal("nothing released")
	}

	if _, ok := r.a.Held(); ok {
		t.Error("still held after a touch")
	}
	if !r.a.hold.sw.Get() {
		t.Error("a touch turned the switch off")
	}
	if r.a.Release() {
		t.Error("a second touch released something")
	}
}

func TestOtherVideoLetsTheHeldAvatarGo(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	holdOn(r)
	spokeOnScreen(t, r)

	r.busy.Store(true)

	if _, ok := r.a.Held(); ok {
		t.Fatal("held while other video has the screen")
	}
	r.busy.Store(false)
	if _, ok := r.a.Held(); ok {
		t.Error("held again once the other video ended")
	}
}

func TestALostLinkLetsTheHeldAvatarGo(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	holdOn(r)
	spokeOnScreen(t, r)

	r.peer().report(webrtc.PeerConnectionStateFailed)

	if _, ok := r.a.Held(); ok {
		t.Fatal("held after the link dropped")
	}
}

func TestTheHoldTurnsItselfOffAfterAMinuteWithoutAnAnnouncement(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	holdOn(r)
	spokeOnScreen(t, r)

	r.clock.pass(defaults.hold - time.Second)
	r.a.reconcile()
	if !r.a.hold.sw.Get() {
		t.Fatal("the hold ended before its timeout")
	}
	r.clock.pass(2 * time.Second)
	r.a.reconcile()

	if r.a.hold.sw.Get() {
		t.Error("the switch is still on after the timeout")
	}
	if _, ok := r.a.Held(); ok {
		t.Error("still held after the timeout")
	}
}

func TestAPrimingPictureIsPendingForAMomentOnly(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)

	r.a.Showing(0)
	if !r.a.Pending(0) {
		t.Fatal("a picture being readied is not pending")
	}
	r.clock.pass(defaults.pending + time.Millisecond)
	if r.a.Pending(0) {
		t.Fatal("still pending after the grace")
	}
}

func TestAPictureThatIsUpIsNotPending(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)

	r.a.Showing(0)
	r.peer().show(rtc.Frame{Key: true})

	if r.a.Pending(0) || !r.a.Showing(0) {
		t.Fatal("pending with the picture up")
	}
}

func TestADroppedLinkIsNotPending(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	r.a.Showing(0)

	r.peer().report(webrtc.PeerConnectionStateFailed)

	if r.a.Pending(0) {
		t.Fatal("pending after the link dropped")
	}
}

// idleFor feeds the held avatar's idle frames for d, with the screen placing it once a second
// when placing is true, as its redraws do with no turn going.
func idleFor(r *rig, d time.Duration, placing bool) {
	for i := range int(d / (100 * time.Millisecond)) {
		r.clock.pass(100 * time.Millisecond)
		r.peer().show(rtc.Frame{})
		if placing && i%10 == 0 {
			r.a.Place(0, display.Rect{W: 10, H: 10}, false)
		}
		r.a.reconcile()
	}
}

func TestAHeldAvatarThatConnectsWhenWokenStaysConnected(t *testing.T) {
	r := newRig(t, woken())
	r.a.Wake(0)
	r.connect(t)
	holdOn(r)
	spokeOnScreen(t, r)

	r.a.Rest(0)
	idleFor(r, defaults.linger+time.Second, true)

	if s, _ := r.a.links[0].snapshot(); s != Connected {
		t.Fatalf("state %s, want the held avatar still connected", s)
	}
	if _, ok := r.a.Held(); !ok {
		t.Error("let go once its linger passed")
	}
}

func TestAHeldAvatarTheScreenStoppedPlacingGoes(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	holdOn(r)
	spokeOnScreen(t, r)

	idleFor(r, defaults.heldUnplaced+500*time.Millisecond, false)

	if !r.scr.hidden {
		t.Error("a held picture nothing placed is still up")
	}
	if _, ok := r.a.Held(); ok {
		t.Error("still held with its picture gone")
	}
}

func TestTheHoldDoesNotTimeOutDuringAReply(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	holdOn(r)

	r.a.replying(true)
	r.clock.pass(defaults.hold + time.Second)
	r.a.reconcile()
	if !r.a.hold.sw.Get() {
		t.Fatal("the hold timed out under a reply")
	}
	r.a.replying(false)
	r.a.reconcile()

	if r.a.hold.sw.Get() {
		t.Error("the hold outlived its timeout once the reply ended")
	}
}

func TestTheScreenHearsTheHeldAvatarChange(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	holdOn(r)
	changes := 0
	defer r.a.HeldChanged.Listen(func(struct{}) { changes++ })()

	spokeOnScreen(t, r)
	if changes != 1 {
		t.Fatalf("%d changes after the reply was held, want 1", changes)
	}
	r.a.Release()

	if changes != 2 {
		t.Errorf("%d changes after the release, want 2", changes)
	}
}

func TestReleasingAHeldAvatarThatIsNotOnScreenTakesNoTouch(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	holdOn(r)
	spokeOnScreen(t, r)
	r.a.links[0].mu.Lock()
	r.a.links[0].rest()
	r.a.links[0].mu.Unlock()

	if r.a.Release() {
		t.Fatal("a touch was taken for an avatar not on screen")
	}
	if r.a.heldSlot() != -1 {
		t.Error("still held")
	}
}

// The reply ends while its show is still on screen, and the next line arrives a second or more
// before its own show starts. The held avatar stays up through both, until that show begins.
func TestAHoldLastsUntilTheNextShowBegins(t *testing.T) {
	r := newRig(t, always())
	r.connect(t)
	holdOn(r)
	spokeOnScreen(t, r)

	r.a.Began()
	if _, ok := r.a.Held(); !ok {
		t.Fatal("the show the reply was held under let it go")
	}
	r.a.Chosen(1, true)
	r.a.Wake(1)
	if _, ok := r.a.Held(); !ok {
		t.Fatal("the next announcement arriving let the held avatar go before its show")
	}
	r.a.Began()

	if _, ok := r.a.Held(); ok {
		t.Fatal("still held after the next show began")
	}
}
