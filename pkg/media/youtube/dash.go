package youtube

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	amp4 "github.com/abema/go-mp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
)

const boxMost = 64 << 20

type packet struct {
	at   time.Duration
	data []byte
}

type demuxer interface {
	next() (packet, error)
	cue(at time.Duration) (off int64, from time.Duration, ok bool, err error)
	restart(off int64)
}

type dashCue struct {
	at  time.Duration
	off int64
}

type dash struct {
	src     io.Reader
	pos     int64
	track   int
	scale   uint32
	config  []byte
	cues    []dashCue
	held    []byte
	pending []packet
}

var _ demuxer = (*dash)(nil)

func newDash(src io.Reader) (*dash, error) {
	d := &dash{src: src}
	var head []byte
	for d.held == nil && d.cues == nil {
		kind, raw, err := d.box()
		if err != nil {
			return nil, fmt.Errorf("mp4: %w", err)
		}
		switch kind {
		case "ftyp", "moov":
			head = append(head, raw...)
		case "sidx":
			if err := d.index(raw); err != nil {
				return nil, err
			}
		case "moof":
			d.held = raw
		}
	}
	var init fmp4.Init
	if err := init.Unmarshal(bytes.NewReader(head)); err != nil {
		return nil, fmt.Errorf("mp4: %w", err)
	}
	for _, t := range init.Tracks {
		c, ok := t.Codec.(*codecs.MPEG4Audio)
		if !ok {
			continue
		}
		config, err := c.Config.Marshal()
		if err != nil {
			return nil, fmt.Errorf("mp4: %w", err)
		}
		d.track, d.scale, d.config = t.ID, t.TimeScale, config
		return d, nil
	}
	return nil, errors.New("mp4: no aac track")
}

func (d *dash) next() (packet, error) {
	for len(d.pending) == 0 {
		if err := d.fragment(); err != nil {
			return packet{}, err
		}
	}
	p := d.pending[0]
	d.pending = d.pending[1:]
	return p, nil
}

func (d *dash) cue(at time.Duration) (int64, time.Duration, bool, error) {
	var (
		off  int64
		from time.Duration
		ok   bool
	)
	for _, c := range d.cues {
		if c.at > at {
			break
		}
		off, from, ok = c.off, c.at, true
	}
	return off, from, ok, nil
}

func (d *dash) restart(off int64) {
	d.pos, d.held, d.pending = off, nil, nil
}

func (d *dash) fragment() error {
	moof := d.held
	d.held = nil
	for moof == nil {
		kind, raw, err := d.box()
		if err != nil {
			return err
		}
		if kind == "moof" {
			moof = raw
		}
	}
	for {
		kind, raw, err := d.box()
		if err != nil {
			return unexpected(err)
		}
		if kind == "mdat" {
			moof = append(moof, raw...)
			break
		}
	}
	var parts fmp4.Parts
	if err := parts.Unmarshal(moof); err != nil {
		return fmt.Errorf("mp4: %w", err)
	}
	for _, p := range parts {
		for _, t := range p.Tracks {
			if t.ID != d.track {
				continue
			}
			ticks := int64(t.BaseTime)
			for _, s := range t.Samples {
				d.pending = append(d.pending, packet{
					at:   inTime(ticks+int64(s.PTSOffset), d.scale),
					data: s.Payload,
				})
				ticks += int64(s.Duration)
			}
		}
	}
	return nil
}

func (d *dash) index(raw []byte) error {
	n := boxHead(raw)
	var s amp4.Sidx
	if _, err := amp4.Unmarshal(
		bytes.NewReader(raw[n:]),
		uint64(len(raw)-n),
		&s,
		amp4.Context{},
	); err != nil {
		return fmt.Errorf("mp4: sidx: %w", err)
	}
	if s.Timescale == 0 {
		return errors.New("mp4: sidx without a timescale")
	}
	off := d.pos + int64(s.GetFirstOffset())
	ticks := int64(s.GetEarliestPresentationTime())
	for _, r := range s.References {
		d.cues = append(d.cues, dashCue{at: inTime(ticks, s.Timescale), off: off})
		off += int64(r.ReferencedSize)
		ticks += int64(r.SubsegmentDuration)
	}
	return nil
}

func (d *dash) box() (string, []byte, error) {
	var head [16]byte
	if _, err := io.ReadFull(d.src, head[:8]); err != nil {
		return "", nil, err
	}
	n := boxHead(head[:8])
	if n > 8 {
		if _, err := io.ReadFull(d.src, head[8:n]); err != nil {
			return "", nil, unexpected(err)
		}
	}
	size := uint64(binary.BigEndian.Uint32(head[:4]))
	if n > 8 {
		size = binary.BigEndian.Uint64(head[8:16])
	}
	if size < uint64(n) || size > boxMost {
		return "", nil, fmt.Errorf("mp4: box %q of %d bytes", head[4:8], size)
	}
	raw := make([]byte, size)
	copy(raw, head[:n])
	if _, err := io.ReadFull(d.src, raw[n:]); err != nil {
		return "", nil, unexpected(err)
	}
	d.pos += int64(size)
	return string(head[4:8]), raw, nil
}

func boxHead(b []byte) int {
	if binary.BigEndian.Uint32(b[:4]) == 1 {
		return 16
	}
	return 8
}

func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

func inTime(ticks int64, scale uint32) time.Duration {
	return time.Duration(ticks * int64(time.Second) / int64(scale))
}

func stereo(b []byte, channels int) []int16 {
	frames := len(b) / (2 * channels)
	out := make([]int16, 2*frames)
	for i := range frames {
		l := int16(binary.LittleEndian.Uint16(b[i*2*channels:]))
		r := l
		if channels > 1 {
			r = int16(binary.LittleEndian.Uint16(b[i*2*channels+2:]))
		}
		out[2*i], out[2*i+1] = l, r
	}
	return out
}
