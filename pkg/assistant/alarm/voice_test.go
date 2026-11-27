package alarm

import (
	"strings"
	"testing"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/audio/tone"
	"github.com/ygelfand/libcountertop/pkg/say"
)

func voiceCall(t *testing.T, values map[string]string) esphome.Call {
	t.Helper()
	args := make(map[string]any, len(values))
	for name, value := range values {
		args[name] = value
	}
	c, err := esphome.NewCall(args)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSpokenClock(t *testing.T) {
	for _, tc := range []struct {
		text string
		hour int
		min  int
	}{
		{"7 am", 7, 0}, {"7:30 PM", 19, 30}, {"12 a.m.", 0, 0},
		{"12 pm", 12, 0}, {"23:05", 23, 5}, {"seven thirty am", 7, 30},
		{"seven oh five pm", 19, 5}, {"seven o'clock am", 7, 0},
		{"quarter past seven am", 7, 15}, {"half past seven pm", 19, 30},
		{"quarter to twelve pm", 11, 45}, {"midnight", 0, 0}, {"noon", 12, 0},
	} {
		t.Run(tc.text, func(t *testing.T) {
			hour, minute, err := spokenClock(tc.text)
			if err != nil || hour != tc.hour || minute != tc.min {
				t.Fatalf("clock = %02d:%02d, %v", hour, minute, err)
			}
		})
	}
	for _, text := range []string{"", "25:00", "7:99", "13 pm", "tomorrow", "seven tomorrow", "7:30 tomorrow", "7.5", "-1", "7:-15"} {
		if _, _, err := spokenClock(text); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
}

func TestVoiceCreatesAndPersistsAlarms(t *testing.T) {
	r := newRig(t)
	action := r.alarms.VoiceAction()
	reply, err := action.Run(voiceCall(t, map[string]string{
		"operation": "set", "time": "seven thirty am every weekday", "name": "Work",
	}))
	if err != nil || reply == "" {
		t.Fatalf("reply = %q, %v", reply, err)
	}
	alarms := r.build().List()
	if len(alarms) != 1 {
		t.Fatalf("saved alarms = %v", alarms)
	}
	al := alarms[0]
	if al.Name != "Work" || al.Hour != 7 || al.Minute != 30 || !al.On ||
		al.Days.String() != "mon,tue,wed,thu,fri" ||
		al.Snooze != Default().Snooze || al.Volume != Default().Volume ||
		al.Sound != Default().Sound {
		t.Fatalf("alarm = %+v", al)
	}
	if len(tone.Wake(al.Sound)) == 0 {
		t.Fatalf("sound %q plays nothing", al.Sound)
	}
}

func TestAlarmSetWithoutSoundRingsTheDefault(t *testing.T) {
	al, err := fromCall(voiceCall(t, map[string]string{"time": "06:30"}))
	if err != nil {
		t.Fatal(err)
	}
	if al.Sound != Default().Sound {
		t.Fatalf("sound = %q, want %q", al.Sound, Default().Sound)
	}
}

func TestVoiceManagesAnExactAlarm(t *testing.T) {
	r := newRig(t)
	al := r.set(Alarm{Name: "Work", Hour: 7, Minute: 30, On: true})
	action := r.alarms.VoiceAction()
	for _, operation := range []string{"list", "next", "disable", "enable", "delete"} {
		reply, err := action.Run(
			voiceCall(t, map[string]string{"operation": operation, "target": "work"}),
		)
		if err != nil || reply == "" {
			t.Fatalf("%s = %q, %v", operation, reply, err)
		}
		list := r.alarms.List()
		switch operation {
		case "disable", "enable":
			if len(list) != 1 || list[0].ID != al.ID || list[0].On != (operation == "enable") {
				t.Fatalf("%s: %+v", operation, list)
			}
		case "delete":
			if len(list) != 0 {
				t.Fatalf("delete: %+v", list)
			}
		}
	}
}

func TestVoiceRefusesAmbiguousAlarmChanges(t *testing.T) {
	r := newRig(t)
	r.set(Alarm{Name: "Work", Hour: 7, On: true})
	r.set(Alarm{Name: "School", Hour: 7, On: true})
	reply, err := r.alarms.VoiceAction().
		Run(voiceCall(t, map[string]string{"operation": "delete", "target": "7 am"}))
	if err != nil || reply == "" || len(r.alarms.List()) != 2 {
		t.Fatalf("reply = %q, %v, alarms = %v", reply, err, r.alarms.List())
	}
	for _, target := range []string{"", "all", "unknown"} {
		_, err := r.alarms.VoiceAction().
			Run(voiceCall(t, map[string]string{"operation": "delete", "target": target}))
		if err != nil || len(r.alarms.List()) != 2 {
			t.Fatalf("target %q changed alarms", target)
		}
	}
}

func TestVoiceCanSnoozeAndStopTheRingingAlarm(t *testing.T) {
	r := newRig(t)
	r.set(Alarm{Name: "Work", Hour: 7, On: true, Snooze: 5 * time.Minute})
	action := r.alarms.VoiceAction()
	r.at(time.Hour)
	if !r.alarms.Ringing() {
		t.Fatal("alarm did not ring")
	}
	if _, err := action.Run(
		voiceCall(t, map[string]string{"operation": "snooze"}),
	); err != nil ||
		r.alarms.Ringing() {
		t.Fatalf("snooze = %v", err)
	}
	r.at(time.Hour + 5*time.Minute)
	if !r.alarms.Ringing() {
		t.Fatal("snoozed alarm did not ring again")
	}
	if _, err := action.Run(
		voiceCall(t, map[string]string{"operation": "stop"}),
	); err != nil ||
		r.alarms.Ringing() {
		t.Fatalf("stop = %v", err)
	}
}

func TestVoiceDoesNotSaveAnInvalidSchedule(t *testing.T) {
	r := newRig(t)
	for _, text := range []string{"tomorrow at seven", "7 am every banana", "7 am on mondayish"} {
		reply, err := r.alarms.VoiceAction().
			Run(voiceCall(t, map[string]string{"operation": "set", "time": text}))
		if err != nil || reply == "" || len(r.alarms.List()) != 0 {
			t.Fatalf("%q = %q, %v, alarms = %v", text, reply, err, r.alarms.List())
		}
	}
}

func TestSpokenSchedules(t *testing.T) {
	for _, tc := range []struct {
		time string
		days string
		want string
	}{
		{"7 am", "", ""},
		{"7 am every day", "", "sun,mon,tue,wed,thu,fri,sat"},
		{"7 am on monday and friday", "", "mon,fri"},
		{"7 am", "weekends", "sun,sat"},
		{"7 am", "monday,monday,friday", "mon,fri"},
	} {
		_, _, days, err := spokenSchedule(tc.time, tc.days)
		if err != nil || days.String() != tc.want {
			t.Errorf("%q, %q = %q, %v", tc.time, tc.days, days.String(), err)
		}
	}
}

func TestVoiceDoesNotDuplicateSavedAlarms(t *testing.T) {
	r := newRig(t)
	r.set(Alarm{Name: "Work", Hour: 7, On: true})
	for _, args := range []map[string]string{
		{"operation": "set", "time": "8 am", "name": "work"},
		{"operation": "set", "time": "7 am"},
	} {
		if _, err := r.alarms.VoiceAction().Run(voiceCall(t, args)); err != nil {
			t.Fatal(err)
		}
		if list := r.alarms.List(); len(list) != 1 || list[0].Hour != 7 {
			t.Fatalf("existing alarm changed: %v", list)
		}
	}
}

func TestAlarmVoiceDeclaration(t *testing.T) {
	action := newRig(t).alarms.VoiceAction()
	if action.Name != "alarm" {
		t.Fatalf("name = %q", action.Name)
	}
	operations := map[string]bool{}
	for _, trigger := range action.Triggers {
		operations[trigger.Args["operation"]] = true
	}
	for _, operation := range strings.Fields("set list next enable disable delete stop snooze") {
		if !operations[operation] {
			t.Errorf("no %s trigger", operation)
		}
	}
}

func TestVoiceSetsAlarmsInTheVoiceLanguage(t *testing.T) {
	defer say.UseVoice("")
	for _, tc := range []struct {
		tag, time, reply string
		hour, minute     int
		days             string
	}{
		{"it", "alle sette e mezza ogni giorno", "ogni giorno", 7, 30, "sun,mon,tue,wed,thu,fri,sat"},
		{"it", "otto meno un quarto nei giorni feriali", "nei giorni feriali", 7, 45, "mon,tue,wed,thu,fri"},
		{"vi", "bảy giờ rưỡi sáng mỗi ngày", "hằng ngày", 7, 30, "sun,mon,tue,wed,thu,fri,sat"},
		{"vi", "6 giờ chiều vào thứ hai và thứ sáu", "Thứ Hai", 18, 0, "mon,fri"},
		{"de", "halb acht werktags", "werktags", 7, 30, "mon,tue,wed,thu,fri"},
		{"de", "viertel vor sieben jeden tag", "jeden Tag", 6, 45, "sun,mon,tue,wed,thu,fri,sat"},
		{"nl", "half acht doordeweeks", "op werkdagen", 7, 30, "mon,tue,wed,thu,fri"},
		{"nl", "kwart over zes in het weekend", "in het weekend", 6, 15, "sun,sat"},
	} {
		t.Run(tc.tag+" "+tc.time, func(t *testing.T) {
			say.UseVoice(tc.tag)
			r := newRig(t)
			reply, err := r.alarms.VoiceAction().
				Run(voiceCall(t, map[string]string{"operation": "set", "time": tc.time}))
			if err != nil {
				t.Fatal(err)
			}
			list := r.alarms.List()
			if len(list) != 1 || list[0].Hour != tc.hour || list[0].Minute != tc.minute ||
				list[0].Days.String() != tc.days {
				t.Fatalf("%q saved %+v, reply %q", tc.time, list, reply)
			}
			if !strings.Contains(reply, tc.reply) {
				t.Errorf("reply %q, want it to say %q", reply, tc.reply)
			}
		})
	}
}
