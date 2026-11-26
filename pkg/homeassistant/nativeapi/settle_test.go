package nativeapi

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"
	"github.com/ygelfand/go-esphome-device/api"
	"google.golang.org/protobuf/proto"
)

const settleTest = 50 * time.Millisecond

// settleRig serves a device that watches Home Assistant's setup, on short timings.
func settleRig(t *testing.T, satellite bool, busy func() bool) (string, *settle) {
	t.Helper()
	s := newSettle(busy)
	s.within, s.wait, s.most, s.every = settleTest, settleTest, 4*settleTest, time.Millisecond
	s.expectVoice(satellite)
	info := esphome.Info{Name: "test"}
	if satellite {
		info.VoiceFeatures = 1
	}
	srv := &esphome.Server{Info: info, Handler: esphome.Chain(s)}
	s.gaveUp = func() { _ = srv.FireEvent(voiceNotSubscribed, nil) }
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = srv.Serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return ln.Addr().String(), s
}

// client is Home Assistant on a plaintext connection.
type client struct {
	t  *testing.T
	nc net.Conn
	r  *bufio.Reader
}

func dial(t *testing.T, addr string) *client {
	t.Helper()
	nc, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = nc.Close() })
	return &client{t: t, nc: nc, r: bufio.NewReader(nc)}
}

func (c *client) send(msgs ...proto.Message) {
	c.t.Helper()
	for _, m := range msgs {
		info, ok := esphome.Lookup(m)
		if !ok {
			c.t.Fatalf("%T has no id", m)
		}
		data, err := proto.Marshal(m)
		if err != nil {
			c.t.Fatal(err)
		}
		frame := []byte{0}
		frame = binary.AppendUvarint(frame, uint64(len(data)))
		frame = binary.AppendUvarint(frame, uint64(info.ID))
		if _, err := c.nc.Write(append(frame, data...)); err != nil {
			c.t.Fatal(err)
		}
	}
}

// listed connects the way Home Assistant does, up to listing the entities.
func (c *client) listed() {
	c.send(&api.HelloRequest{ClientInfo: "Home Assistant"}, &api.DeviceInfoRequest{},
		&api.ListEntitiesRequest{})
}

// event waits up to d for the device to fire an event, and names it.
func (c *client) event(d time.Duration) string {
	c.t.Helper()
	if err := c.nc.SetReadDeadline(time.Now().Add(d)); err != nil {
		c.t.Fatal(err)
	}
	for {
		var head [3]uint64
		for i := range head {
			v, err := binary.ReadUvarint(c.r)
			if err != nil {
				return ""
			}
			head[i] = v
		}
		data := make([]byte, head[1])
		if _, err := io.ReadFull(c.r, data); err != nil {
			return ""
		}
		msg, ok := esphome.NewMessage(uint32(head[2]))
		if !ok || proto.Unmarshal(data, msg) != nil {
			continue
		}
		if ev, ok := msg.(*api.HomeassistantActionRequest); ok && ev.GetIsEvent() {
			return ev.GetService()
		}
	}
}

// closedWithin reports whether the device dropped the connection within d.
func (c *client) closedWithin(d time.Duration) bool {
	c.t.Helper()
	if err := c.nc.SetReadDeadline(time.Now().Add(d)); err != nil {
		c.t.Fatal(err)
	}
	for {
		if _, err := c.r.ReadByte(); err != nil {
			var timeout net.Error
			return !errors.As(err, &timeout) || !timeout.Timeout()
		}
	}
}

func TestRegressionAStalledSetupWithNoStatesIsDroppedSoHomeAssistantStartsOver(t *testing.T) {
	addr, _ := settleRig(t, true, nil)
	ha := dial(t, addr)

	ha.listed()
	ha.send(&api.SubscribeVoiceAssistantRequest{Subscribe: true})

	if !ha.closedWithin(20 * settleTest) {
		t.Fatal("kept a connection whose entities never got their states")
	}
}

func TestASetupThatSubscribesEverythingIsKept(t *testing.T) {
	addr, _ := settleRig(t, true, nil)
	ha := dial(t, addr)

	ha.listed()
	ha.send(&api.SubscribeStatesRequest{}, &api.SubscribeVoiceAssistantRequest{Subscribe: true})

	if ha.closedWithin(4 * settleTest) {
		t.Fatal("dropped a connection that had settled")
	}
}

func TestADeviceWithoutVoiceIsSettledByTheStatesAlone(t *testing.T) {
	addr, _ := settleRig(t, false, nil)
	ha := dial(t, addr)

	ha.listed()
	ha.send(&api.SubscribeStatesRequest{})

	if ha.closedWithin(4 * settleTest) {
		t.Fatal("dropped a device without voice for not subscribing to voice")
	}
}

// A client that never lists the entities, such as a log viewer, is not Home Assistant's setup.
func TestAClientThatNeverListsTheEntitiesIsLeftAlone(t *testing.T) {
	addr, _ := settleRig(t, true, nil)
	ha := dial(t, addr)

	ha.send(&api.HelloRequest{ClientInfo: "logs"}, &api.SubscribeLogsRequest{})

	if ha.closedWithin(4 * settleTest) {
		t.Fatal("dropped a client that was not setting the device up")
	}
}

// Home Assistant with its assist satellite off never subscribes to voice either, so a missing
// voice subscription costs one reconnect, not one every few minutes.
func TestRegressionMissingVoiceIsRetriedOnlyOnce(t *testing.T) {
	addr, _ := settleRig(t, true, nil)

	first := dial(t, addr)
	first.listed()
	first.send(&api.SubscribeStatesRequest{})
	if !first.closedWithin(20 * settleTest) {
		t.Fatal("did not retry a setup that never subscribed to voice")
	}

	second := dial(t, addr)
	second.listed()
	second.send(&api.SubscribeStatesRequest{})
	if second.closedWithin(4 * settleTest) {
		t.Fatal("kept dropping a Home Assistant that does not subscribe to voice")
	}
}

// Giving up tells Home Assistant, so an automation there can reload the device's entry.
func TestGivingUpOnVoiceFiresAnEvent(t *testing.T) {
	addr, _ := settleRig(t, true, nil)
	first := dial(t, addr)
	first.listed()
	first.send(&api.SubscribeStatesRequest{})
	if !first.closedWithin(20 * settleTest) {
		t.Fatal("did not retry a setup that never subscribed to voice")
	}

	second := dial(t, addr)
	second.listed()
	second.send(&api.SubscribeStatesRequest{})

	if got := second.event(20 * settleTest); got != voiceNotSubscribed {
		t.Fatalf("event %q, want %q", got, voiceNotSubscribed)
	}
}

func TestAStalledSetupIsNotDroppedDuringATurn(t *testing.T) {
	var busy atomic.Bool
	busy.Store(true)
	addr, _ := settleRig(t, true, busy.Load)
	ha := dial(t, addr)

	ha.listed()
	ha.send(&api.SubscribeVoiceAssistantRequest{Subscribe: true})
	if ha.closedWithin(4 * settleTest) {
		t.Fatal("dropped the connection in the middle of a turn")
	}
	busy.Store(false)

	if !ha.closedWithin(20 * settleTest) {
		t.Fatal("never dropped the stalled setup after the turn")
	}
}

func TestEachStalledSetupWaitsLongerUpToTheCap(t *testing.T) {
	addr, s := settleRig(t, false, nil)

	for _, want := range []time.Duration{2, 4, 4} {
		ha := dial(t, addr)
		ha.listed()
		if !ha.closedWithin(20 * settleTest) {
			t.Fatal("kept a connection whose entities never got their states")
		}
		s.mu.Lock()
		got := s.wait
		s.mu.Unlock()
		if got != want*settleTest {
			t.Fatalf("next wait %s, want %s", got, want*settleTest)
		}
	}

	ha := dial(t, addr)
	ha.listed()
	ha.send(&api.SubscribeStatesRequest{})
	// The device handles the subscription on its own read loop.
	for give := time.Now().Add(time.Second); ; {
		s.mu.Lock()
		got := s.wait
		s.mu.Unlock()
		if got == settleTest {
			return
		}
		if time.Now().After(give) {
			t.Fatalf("wait %s after a settled setup, want it back to %s", got, settleTest)
		}
		time.Sleep(time.Millisecond)
	}
}
