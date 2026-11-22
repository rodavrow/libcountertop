package airplay

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
)

const (
	quietestDB = -30
	mutedDB    = -144
)

type Volume interface {
	Level() int
	SetLevel(level int)
	Muted() bool
	Mute(on bool)
	Listen(changed func(level int)) (stop func())
}

func dbOf(p []byte) (float32, error) {
	if len(p) != 4 {
		return 0, fmt.Errorf("airplay: %d byte volume", len(p))
	}
	return math.Float32frombits(binary.LittleEndian.Uint32(p)), nil
}

func levelOf(db float32) int {
	return min(max(int(math.Round(float64(db-quietestDB)/-quietestDB*100)), 0), 100)
}

func decibels(level int, muted bool) float32 {
	if muted || level <= 0 {
		return mutedDB
	}
	return float32(min(level, 100))/100*-quietestDB + quietestDB
}

func (s *screening) loudness(db float32) {
	v := s.r.opts.Volume
	if v == nil {
		return
	}
	if db <= mutedDB {
		slog.Info("airplay volume", "client", s.client, "muted", true)
		v.Mute(true)
		return
	}
	level := levelOf(db)
	slog.Info("airplay volume", "client", s.client, "db", db, "level", level)
	v.Mute(false)
	v.SetLevel(level)
}

func (s *screening) muted(on bool) {
	if v := s.r.opts.Volume; v != nil {
		slog.Info("airplay volume", "client", s.client, "muted", on)
		v.Mute(on)
	}
}

func (s *screening) tellLevel() {
	v := s.r.opts.Volume
	if v == nil {
		return
	}
	db := decibels(v.Level(), v.Muted())
	s.write.Lock()
	defer s.write.Unlock()
	b := binary.LittleEndian.AppendUint32(nil, math.Float32bits(db))
	if err := writeMessage(s.conn, msgLevel, b); err != nil {
		slog.Debug("airplay level not sent", "err", err)
	}
}
