package avatar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/ygelfand/libcountertop/pkg/media/pcm"
)

// handbackRate is the rate of the audio handed back for the device to speak itself.
const handbackRate = 16000

var errIdle = errors.New("avatar: the backup clip stopped arriving")

// backup is a fed reply's clip, fetched from Home Assistant while the avatar speaks it, so that a
// hand-back has the rest at once. Home Assistant streams it as the reply is spoken, pauses for a
// tool call included, so the fetch has no total timeout: only a gap with no bytes ends it.
type backup struct {
	// first is closed once the first bytes arrive, or the fetch ends without any.
	first chan struct{}
	done  chan struct{}
	stop  context.CancelFunc

	// Set before done is closed.
	kind string
	body []byte
	err  error
}

// fetchBackup starts fetching the clip at url; idle is the longest gap without bytes.
func (a *Avatars) fetchBackup(ctx context.Context, url string, idle time.Duration) *backup {
	ctx, stop := context.WithCancel(ctx)
	b := &backup{first: make(chan struct{}), done: make(chan struct{}), stop: stop}
	// The device's client may carry a total timeout, which a paused stream would run into.
	client := *a.o.Client
	client.Timeout = 0
	go b.fetch(ctx, &client, url, idle)
	return b
}

func (b *backup) fetch(ctx context.Context, client *http.Client, url string, idle time.Duration) {
	var once sync.Once
	arrived := func() { once.Do(func() { close(b.first) }) }
	defer close(b.done)
	defer arrived()

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	quiet := time.AfterFunc(idle, func() { cancel(errIdle) })
	defer quiet.Stop()

	b.err = func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return cause(ctx, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: %s", url, resp.Status)
		}
		b.kind = resp.Header.Get("Content-Type")
		buf := make([]byte, 32<<10)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				quiet.Reset(idle)
				b.body = append(b.body, buf[:n]...)
				arrived()
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return cause(ctx, err)
			}
		}
	}()
}

// cause names why ctx ended rather than the read it broke off.
func cause(ctx context.Context, err error) error {
	if c := context.Cause(ctx); c != nil {
		return c
	}
	return err
}

// rest waits for the whole clip and returns it from from on.
func (b *backup) rest(ctx context.Context, from time.Duration) ([]int16, error) {
	select {
	case <-b.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if b.err != nil {
		return nil, b.err
	}
	samples, err := pcm.Decode(b.kind, b.body, handbackRate)
	if err != nil {
		return nil, err
	}
	skip := min(int(from*handbackRate/time.Second), len(samples))
	return samples[skip:], nil
}
