package homeassistant

import (
	"strings"
	"testing"
)

func TestEachSlotFindsItsOwnSelect(t *testing.T) {
	const mac = "AA:BB:CC:DD:EE:FF"
	for _, tc := range []struct {
		base string
		slot int
		want string
	}{
		{"_wake_word", 0, "_wake_word$"},
		{"_wake_word", 1, "_wake_word_2$"},
		{"_assistant", 0, "_assistant$"},
		{"_assistant", 1, "_assistant_2$"},
	} {
		got := slotSelect(mac, tc.base, tc.slot)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s slot %d template %q does not match %q", tc.base, tc.slot+1, got, tc.want)
		}
		if !strings.Contains(got, "'aa:bb:cc:dd:ee:ff'") {
			t.Errorf(
				"%s slot %d template does not match the device by its lower-case MAC",
				tc.base,
				tc.slot+1,
			)
		}
	}
}

// The device has other selects whose ids end in _assistant, such as the one that names the next
// announcement's assistant. Only the select offering Home Assistant's own option is the real one.
func TestASelectIsFoundByAnOptionOnlyItOffers(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"_assistant", "'preferred' in (state_attr(e, 'options') or [])"},
		{"_wake_word", "'no_wake_word' in (state_attr(e, 'options') or [])"},
	} {
		if got := slotSelect("AA:BB:CC:DD:EE:FF", tc.base, 0); !strings.Contains(got, tc.want) {
			t.Errorf("%s template %q does not ask for %q", tc.base, got, tc.want)
		}
	}
}
