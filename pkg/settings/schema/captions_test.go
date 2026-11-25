package schema

import (
	"encoding/json"
	"testing"
)

func TestCaptionsAreShownUnlessSwitchedOff(t *testing.T) {
	if !DefaultWake().ShowCaptions {
		t.Fatal("a new device shows no captions")
	}

	saved := DefaultWake()
	if err := json.Unmarshal([]byte(`{"arbitration": 50}`), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.ShowCaptions {
		t.Fatal("settings saved before the setting existed switched it off")
	}

	if err := json.Unmarshal([]byte(`{"show_captions": false}`), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ShowCaptions {
		t.Fatal("switching it off did not load")
	}
}
