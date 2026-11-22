package airplay

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ygelfand/libcountertop/pkg/media/playback"
)

const (
	version    = 7
	maxPayload = 4 << 20

	msgReady      = 'R'
	msgClient     = 'C'
	msgMirror     = 'M'
	msgFrame      = 'V'
	msgEnded      = 'E'
	msgDisconnect = 'X'
	msgAudio      = 'A'
	msgSound      = 'S'
	msgPlay       = 'P'
	msgScrub      = 'K'
	msgRate       = 'T'
	msgStop       = 'Z'
	msgPosition   = 'O'
	msgPin        = 'N'
	msgPaired     = 'D'
	msgVolume     = 'L'
	msgMute       = 'U'
	msgLevel      = 'B'

	codecH264 = 1
	codecH265 = 2
)

type state int64

const (
	stateLoading state = iota
	statePlaying
	statePaused
	stateOver
	stateEnded
)

var (
	TCPPorts = []int{7000, 7100}
	UDPPorts = []int{6000, 6001, 7011}
)

type message struct {
	kind    uint32
	payload []byte
}

func writeMessage(w io.Writer, kind uint32, payload []byte) error {
	buf := make([]byte, 8+len(payload))
	binary.LittleEndian.PutUint32(buf[0:], kind)
	binary.LittleEndian.PutUint32(buf[4:], uint32(len(payload)))
	copy(buf[8:], payload)
	_, err := w.Write(buf)
	return err
}

func readMessage(r io.Reader) (message, error) {
	var head [8]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return message{}, err
	}
	m := message{kind: binary.LittleEndian.Uint32(head[0:])}
	n := binary.LittleEndian.Uint32(head[4:])
	if n > maxPayload {
		return message{}, fmt.Errorf("airplay: %d byte message", n)
	}
	m.payload = make([]byte, n)
	if _, err := io.ReadFull(r, m.payload); err != nil {
		return message{}, err
	}
	return m, nil
}

type ready struct {
	version, port uint32
	raop, airplay announced
}

type announced struct {
	instance string
	text     []string
}

const (
	readyHead     = 24
	raopSuffix    = "._raop._tcp.local"
	airplaySuffix = "._airplay._tcp.local"
)

func readyOf(p []byte) (ready, error) {
	if len(p) < 8 {
		return ready{}, fmt.Errorf("airplay: %d byte ready", len(p))
	}
	r := ready{version: binary.LittleEndian.Uint32(p[0:]), port: binary.LittleEndian.Uint32(p[4:])}
	if r.version != version {
		return r, nil
	}
	if len(p) < readyHead {
		return ready{}, fmt.Errorf("airplay: %d byte ready", len(p))
	}
	var sizes [4]int
	total := readyHead
	for i := range sizes {
		sizes[i] = int(binary.LittleEndian.Uint32(p[8+4*i:]))
		total += sizes[i]
	}
	if total != len(p) {
		return ready{}, fmt.Errorf("airplay: ready says %d bytes, has %d", total, len(p))
	}
	var parts [4][]byte
	at := readyHead
	for i, n := range sizes {
		parts[i] = p[at : at+n]
		at += n
	}
	raopText, err := textOf(parts[2])
	if err != nil {
		return ready{}, err
	}
	airplayText, err := textOf(parts[3])
	if err != nil {
		return ready{}, err
	}
	r.raop = announced{strings.TrimSuffix(string(parts[0]), raopSuffix), raopText}
	r.airplay = announced{strings.TrimSuffix(string(parts[1]), airplaySuffix), airplayText}
	return r, nil
}

func textOf(wire []byte) ([]string, error) {
	var out []string
	for len(wire) > 0 {
		n := int(wire[0])
		if 1+n > len(wire) {
			return nil, fmt.Errorf("airplay: TXT entry of %d bytes in %d", n, len(wire)-1)
		}
		out = append(out, string(wire[1:1+n]))
		wire = wire[1+n:]
	}
	return out, nil
}

type mirroring struct {
	codec, width, height uint32
}

func mirroringOf(p []byte) (mirroring, error) {
	if len(p) < 12 {
		return mirroring{}, fmt.Errorf("airplay: %d byte mirror", len(p))
	}
	return mirroring{
		codec:  binary.LittleEndian.Uint32(p[0:]),
		width:  binary.LittleEndian.Uint32(p[4:]),
		height: binary.LittleEndian.Uint32(p[8:]),
	}, nil
}

func (m mirroring) picture() (uint32, bool) {
	switch m.codec {
	case codecH264:
		return playback.H264, true
	case codecH265:
		return playback.HEVC, true
	}
	return 0, false
}

type frame struct {
	at            time.Duration
	width, height int
	data          []byte
}

const frameHead = 16

func frameOf(p []byte) (frame, error) {
	if len(p) < frameHead {
		return frame{}, fmt.Errorf("airplay: %d byte frame", len(p))
	}
	us := binary.LittleEndian.Uint64(p[0:])
	return frame{
		at:     time.Duration(us) * time.Microsecond,
		width:  int(binary.LittleEndian.Uint32(p[8:])),
		height: int(binary.LittleEndian.Uint32(p[12:])),
		data:   p[frameHead:],
	}, nil
}

type play struct {
	at       time.Duration
	location string
}

func playOf(p []byte) (play, error) {
	if len(p) < 8 {
		return play{}, fmt.Errorf("airplay: %d byte play", len(p))
	}
	at := time.Duration(int64(binary.LittleEndian.Uint64(p[0:]))) * time.Microsecond
	if len(p) == 8 {
		return play{}, errors.New("airplay: play without a location")
	}
	return play{at: max(at, 0), location: string(p[8:])}, nil
}

func scrubOf(p []byte) (time.Duration, error) {
	if len(p) != 8 {
		return 0, fmt.Errorf("airplay: %d byte scrub", len(p))
	}
	at := time.Duration(int64(binary.LittleEndian.Uint64(p[0:]))) * time.Microsecond
	return max(at, 0), nil
}

func rateOf(p []byte) (playing bool, err error) {
	if len(p) != 4 {
		return false, fmt.Errorf("airplay: %d byte rate", len(p))
	}
	return binary.LittleEndian.Uint32(p[0:]) != 0, nil
}

type position struct {
	at, length time.Duration
	state      state
}

func (p position) bytes() []byte {
	b := binary.LittleEndian.AppendUint64(nil, uint64(p.at.Microseconds()))
	b = binary.LittleEndian.AppendUint64(b, uint64(p.length.Microseconds()))
	return binary.LittleEndian.AppendUint64(b, uint64(p.state))
}

type audio struct {
	rate, channels int
	due            time.Duration
	pcm            []int16
}

const audioHead = 12

func audioOf(p []byte) (audio, error) {
	if len(p) < audioHead || (len(p)-audioHead)%2 != 0 {
		return audio{}, fmt.Errorf("airplay: %d byte audio", len(p))
	}
	a := audio{
		rate:     int(binary.LittleEndian.Uint32(p[0:])),
		channels: int(binary.LittleEndian.Uint32(p[4:])),
		due:      time.Duration(int32(binary.LittleEndian.Uint32(p[8:]))) * time.Microsecond,
		pcm:      make([]int16, (len(p)-audioHead)/2),
	}
	if a.rate <= 0 || a.channels <= 0 {
		return audio{}, fmt.Errorf("airplay: audio at %d Hz x%d", a.rate, a.channels)
	}
	for i := range a.pcm {
		a.pcm[i] = int16(binary.LittleEndian.Uint16(p[audioHead+2*i:]))
	}
	return a, nil
}
