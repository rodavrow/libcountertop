package schema

import (
	"github.com/ygelfand/libcountertop/pkg/audio/ducking"
	"github.com/ygelfand/libcountertop/pkg/say"
)

type Media struct {
	DuckDB     float64       `json:"duck_db"`
	Buttons    VolumeButtons `json:"buttons"`
	Player     PlayerStart   `json:"player"`
	Mini       bool          `json:"mini"`
	Background bool          `json:"background"`
	Autoplay   bool          `json:"autoplay"`
}

func DefaultMedia() Media {
	return Media{DuckDB: ducking.DefaultDB, Buttons: ButtonsDynamic, Player: PlayerFull, Mini: true}
}

type PlayerStart string

const (
	PlayerFull PlayerStart = "full"
	PlayerNone PlayerStart = "none"
)

func (p PlayerStart) Label() string {
	switch p {
	case PlayerFull:
		return say.T("player.full")
	case PlayerNone:
		return say.T("player.none")
	}
	return string(p)
}

func PlayerStarts() []PlayerStart { return []PlayerStart{PlayerFull, PlayerNone} }

type VolumeButtons string

const (
	ButtonsStatic  VolumeButtons = "static"
	ButtonsDynamic VolumeButtons = "dynamic"
)

func (b VolumeButtons) Label() string {
	switch b {
	case ButtonsStatic:
		return say.T("buttons.static")
	case ButtonsDynamic:
		return say.T("buttons.dynamic")
	}
	return string(b)
}

func VolumeButtonModes() []VolumeButtons { return []VolumeButtons{ButtonsStatic, ButtonsDynamic} }
