package youtube

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"
	"time"

	amp4 "github.com/abema/go-mp4"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
	aac "github.com/tphakala/go-aac"
	aacpcm "github.com/tphakala/go-aac/pcm"

	"github.com/ygelfand/libcountertop/pkg/audio/resample"
)

const (
	dashRate  = 44100
	dashFrame = 1024
)

type dashFile struct {
	data   []byte
	starts []int64
	aus    [][]byte
	split  int
}

func tone(t *testing.T, frames int) ([]byte, [][]byte) {
	t.Helper()
	enc, err := aacpcm.NewFrameEncoder(aacpcm.Config{
		SampleRate: dashRate,
		BitDepth:   16,
		Channels:   2,
		Bitrate:    128000,
		Coder:      aac.CoderFast,
	})
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]byte, frames*dashFrame*4)
	for i := range frames * dashFrame {
		v := uint16(int16(8000 * math.Sin(2*math.Pi*440*float64(i)/dashRate)))
		binary.LittleEndian.PutUint16(pcm[4*i:], v)
		binary.LittleEndian.PutUint16(pcm[4*i+2:], v)
	}
	var aus [][]byte
	keep := func(au []byte, _ int) error {
		aus = append(aus, bytes.Clone(au))
		return nil
	}
	if err := enc.EncodeInterleaved(pcm, keep); err != nil {
		t.Fatal(err)
	}
	if err := enc.Flush(keep); err != nil {
		t.Fatal(err)
	}
	return enc.AudioSpecificConfig(), aus
}

func newDashFile(t *testing.T, frames int) dashFile {
	t.Helper()
	asc, aus := tone(t, frames)
	var config mpeg4audio.AudioSpecificConfig
	if err := config.Unmarshal(asc); err != nil {
		t.Fatal(err)
	}
	var head seekablebuffer.Buffer
	init := fmp4.Init{Tracks: []*fmp4.InitTrack{{
		ID:        1,
		TimeScale: dashRate,
		Codec:     &codecs.MPEG4Audio{Config: config},
	}}}
	if err := init.Marshal(&head); err != nil {
		t.Fatal(err)
	}

	split := len(aus) / 2
	var frags [][]byte
	var refs []amp4.SidxReference
	for i, group := range [][][]byte{aus[:split], aus[split:]} {
		samples := make([]*fmp4.Sample, len(group))
		for j, au := range group {
			samples[j] = &fmp4.Sample{Duration: dashFrame, Payload: au}
		}
		var b seekablebuffer.Buffer
		part := fmp4.Part{SequenceNumber: uint32(i + 1), Tracks: []*fmp4.PartTrack{{
			ID:       1,
			BaseTime: uint64(i * split * dashFrame),
			Samples:  samples,
		}}}
		if err := part.Marshal(&b); err != nil {
			t.Fatal(err)
		}
		frags = append(frags, b.Bytes())
		refs = append(refs, amp4.SidxReference{
			ReferencedSize:     uint32(b.Len()),
			SubsegmentDuration: uint32(len(group) * dashFrame),
			StartsWithSAP:      true,
		})
	}

	var body bytes.Buffer
	sidx := amp4.Sidx{
		ReferenceID:    1,
		Timescale:      dashRate,
		ReferenceCount: uint16(len(refs)),
		References:     refs,
	}
	if _, err := amp4.Marshal(&body, &sidx, amp4.Context{}); err != nil {
		t.Fatal(err)
	}
	file := bytes.Clone(head.Bytes())
	file = binary.BigEndian.AppendUint32(file, uint32(8+body.Len()))
	file = append(file, "sidx"...)
	file = append(file, body.Bytes()...)
	f := dashFile{aus: aus, split: split}
	for _, frag := range frags {
		f.starts = append(f.starts, int64(len(file)))
		file = append(file, frag...)
	}
	f.data = file
	return f
}

type seekable struct{ *bytes.Reader }

func (s seekable) From(off int64) {
	if _, err := s.Seek(off, io.SeekStart); err != nil {
		panic(err)
	}
}

func (s seekable) Close() error { return nil }

func TestADashStreamGivesEachAACFrameAtItsTimeAndCuesAtAFragment(t *testing.T) {
	f := newDashFile(t, 20)
	src := bytes.NewReader(f.data)
	d, err := newDash(src)
	if err != nil {
		t.Fatal(err)
	}
	if d.scale != dashRate || len(d.config) == 0 {
		t.Fatalf("scale %d, config %x", d.scale, d.config)
	}
	for i, au := range f.aus {
		p, err := d.next()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if want := inTime(int64(i*dashFrame), dashRate); p.at != want || !bytes.Equal(p.data, au) {
			t.Fatalf(
				"frame %d at %v with %d bytes, want %v with %d",
				i,
				p.at,
				len(p.data),
				want,
				len(au),
			)
		}
	}
	if _, err := d.next(); !errors.Is(err, io.EOF) {
		t.Fatalf("after the last frame: %v", err)
	}

	second := inTime(int64(f.split*dashFrame), dashRate)
	off, from, ok, err := d.cue(second + time.Millisecond)
	if err != nil || !ok || off != f.starts[1] || from != second {
		t.Fatalf("cue %d at %v (%v %v), want %d at %v", off, from, ok, err, f.starts[1], second)
	}
	if off, _, _, _ := d.cue(0); off != f.starts[0] {
		t.Fatalf("cue at the start %d, want %d", off, f.starts[0])
	}
	seekable{src}.From(off)
	d.restart(off)
	p, err := d.next()
	if err != nil || p.at != second || !bytes.Equal(p.data, f.aus[f.split]) {
		t.Fatalf("after restarting: at %v, %v", p.at, err)
	}
}

func TestAnAACTrackPlaysAt48kAndStartsFromTheCuedFragment(t *testing.T) {
	f := newDashFile(t, 40)
	r := NewResolver(nil, nil)
	r.Resample = func(from int) func([]int16) []int16 {
		return resample.NewRational(from, rate, channels).Run
	}
	length := inTime(int64(len(f.aus)*dashFrame), dashRate)

	whole, err := newTrack(
		r,
		Info{ID: "a", Codec: codecAAC},
		seekable{bytes.NewReader(f.data)},
		Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	got, loud := drain(t, whole)
	want := int(length * rate / time.Second)
	if got < want*9/10 || got > want*11/10 || loud < 1000 {
		t.Fatalf("played %d frames peaking at %d, want about %d and a tone", got, loud, want)
	}

	at := inTime(int64(f.split*dashFrame), dashRate) + 50*time.Millisecond
	late, err := newTrack(
		r,
		Info{ID: "a", Codec: codecAAC},
		seekable{bytes.NewReader(f.data)},
		Options{At: at},
	)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = drain(t, late)
	want = int((length - at) * rate / time.Second)
	if got < want*8/10 || got > want*12/10 {
		t.Fatalf("from %v played %d frames, want about %d", at, got, want)
	}
}

func drain(t *testing.T, tr *Track) (int, int) {
	t.Helper()
	pcm := make([]int16, 4096)
	frames, peak := 0, 0
	for {
		n, err := tr.Read(t.Context(), pcm)
		for _, v := range pcm[:n] {
			peak = max(peak, int(v), -int(v))
		}
		frames += n / channels
		if errors.Is(err, io.EOF) {
			return frames, peak
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}
