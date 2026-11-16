//go:build unix

package touch

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

// blockingPipe is a pipe the runtime does not poll, like the touchscreen on some panels: closing
// its read end does not wake a read already waiting on it.
func blockingPipe(t *testing.T) (r, w *os.File) {
	t.Helper()
	var fds [2]int
	if err := syscall.Pipe(fds[:]); err != nil {
		t.Fatal(err)
	}
	return os.NewFile(uintptr(fds[0]), "touch"), os.NewFile(uintptr(fds[1]), "writer")
}

func TestRunEndsWithItsContextEvenWhenTheReadCannotBeWoken(t *testing.T) {
	r, w := blockingPipe(t)
	// Closing the writer is what finally lets the stranded read go, once the test is over.
	t.Cleanup(func() { _ = w.Close() })

	s := New("", landscape())
	s.f = r

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	time.Sleep(50 * time.Millisecond) // let Run reach the read before the context ends
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run still waits on a read nothing will wake after its context ended")
	}
}

func TestEventsAfterTheContextEndsAreNotDelivered(t *testing.T) {
	r, w := blockingPipe(t)
	t.Cleanup(func() { _ = w.Close() })

	s := New("", landscape())
	s.f = r
	got := make(chan Contact, 4)
	s.Contacts.Listen(func(c Contact) { got <- c })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	time.Sleep(50 * time.Millisecond) // let Run reach the read before the context ends
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run still waits on a read nothing will wake after its context ended")
	}

	_, err := w.Write(touchDown())
	if errors.Is(err, syscall.EPIPE) {
		return // the context ended before the read began, so the device really closed
	}
	if err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-got:
		t.Errorf("a touch after the context ended was delivered: %v", c)
	case <-time.After(200 * time.Millisecond):
	}
}

func touchDown() []byte {
	var b bytes.Buffer
	for _, e := range []rawEvent{
		ev(evAbs, absMTTrackingID, 7),
		ev(evAbs, absMTPositionX, 600),
		ev(evAbs, absMTPositionY, 960),
		syn(),
	} {
		_ = binary.Write(&b, binary.LittleEndian, e)
	}
	return b.Bytes()
}
