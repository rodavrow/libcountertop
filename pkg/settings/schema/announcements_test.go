package schema

import (
	"encoding/json"
	"testing"
)

func TestAnnouncementsAreAnimatedUnlessSwitchedOff(t *testing.T) {
	if !DefaultWake().AnimateAnnouncements {
		t.Fatal("a new device does not animate announcements")
	}

	saved := DefaultWake()
	if err := json.Unmarshal([]byte(`{"arbitration": 50}`), &saved); err != nil {
		t.Fatal(err)
	}
	if !saved.AnimateAnnouncements {
		t.Fatal("settings saved before the setting existed switched it off")
	}

	if err := json.Unmarshal([]byte(`{"animate_announcements": false}`), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.AnimateAnnouncements {
		t.Fatal("switching it off did not load")
	}
}
