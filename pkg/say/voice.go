package say

import "github.com/nicksnyder/go-i18n/v2/i18n"

func UseVoice(want string) string {
	mu.Lock()
	defer mu.Unlock()

	if want == "" {
		want = English.String()
	}

	spoken = i18n.NewLocalizer(bundle, want, English.String())
	voice = settled(want)

	return voice
}

func Voice() string {
	mu.RLock()
	defer mu.RUnlock()
	return voice
}

func Speak(id string) string { return look(&spoken, id, &i18n.LocalizeConfig{MessageID: id}) }

func SpeakF(id string, with map[string]any) string {
	return look(&spoken, id, &i18n.LocalizeConfig{MessageID: id, TemplateData: with})
}
