package h264

import "fmt"

type Profile uint32

const (
	Baseline Profile = 0x01
	High     Profile = 0x08
)

func (p Profile) String() string {
	switch p {
	case Baseline:
		return "Baseline"
	case High:
		return "High"
	}
	return fmt.Sprintf("profile %d", uint32(p))
}

func (p Profile) LevelID() string {
	if p == High {
		return "640032"
	}
	return "42e01f"
}
