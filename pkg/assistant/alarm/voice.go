package alarm

import (
	"fmt"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/ygelfand/libcountertop/pkg/actions"
	"github.com/ygelfand/libcountertop/pkg/say"
)

func (a *Alarms) VoiceAction() actions.Action {
	trigger := func(key, operation string) actions.Trigger {
		return actions.Trigger{
			Key:  "actions.alarm.sentence." + key,
			Args: map[string]string{"operation": operation},
		}
	}
	return actions.Action{
		Name: "alarm", Title: say.T("actions.alarm"),
		Args: []esphome.Arg{
			{
				Name:        "operation",
				Type:        esphome.ArgString,
				Description: "set, list, next, enable, disable, delete, stop or snooze.",
				Example:     "set",
			},
			{Name: "time", Type: esphome.ArgString, Description: "For set.", Example: "7 am"},
			{Name: "days", Type: esphome.ArgString, Description: "For set; empty rings once."},
			{Name: "name", Type: esphome.ArgString, Description: "For set."},
			{
				Name:        "target",
				Type:        esphome.ArgString,
				Description: "The alarm to enable, disable or delete.",
			},
		},
		Triggers: []actions.Trigger{
			trigger("set_named", "set"),
			trigger("set", "set"),
			trigger("list", "list"),
			trigger("list_what", "list"),
			trigger("next", "next"),
			trigger("enable", "enable"),
			trigger("disable", "disable"),
			trigger("delete", "delete"),
			trigger("stop", "stop"),
			trigger("snooze", "snooze"),
			trigger("snooze_bare", "snooze"),
		},
		Run: a.voice,
	}
}

func (a *Alarms) voice(c esphome.Call) (string, error) {
	switch operation := c.String("operation"); operation {
	case "set":
		hour, minute, days, err := spokenSchedule(c.String("time"), c.String("days"))
		if err != nil {
			return say.Speak("actions.alarm.time"), nil
		}
		name := strings.TrimSpace(c.String("name"))
		for _, al := range a.List() {
			if (name != "" && strings.EqualFold(al.Name, name)) ||
				(name == "" && al.Hour == hour && al.Minute == minute && al.Days.String() == days.String()) {
				return say.SpeakF(
					"actions.alarm.exists",
					map[string]any{"Alarm": alarmLabel(al)},
				), nil
			}
		}
		al, err := a.Set(Alarm{
			Name: name, Hour: hour, Minute: minute, Days: days, On: true,
			Snooze: 5 * time.Minute, Volume: VolumeStatic,
		})
		if err != nil {
			return "", err
		}
		return say.SpeakF("actions.alarm.set", map[string]any{"Alarm": alarmLabel(al)}), nil
	case "list":
		list := a.List()
		if len(list) == 0 {
			return say.Speak("actions.alarm.empty"), nil
		}
		labels := make([]string, 0, len(list))
		for _, al := range list {
			label := alarmLabel(al)
			if !al.On {
				label = say.SpeakF("actions.alarm.off", map[string]any{"Alarm": label})
			}
			labels = append(labels, label)
		}
		return strings.Join(labels, ". "), nil
	case "next":
		al, at, ok := a.Upcoming()
		if !ok {
			return say.Speak("actions.alarm.none"), nil
		}
		return say.SpeakF(
			"actions.alarm.next",
			map[string]any{
				"Alarm": alarmLabel(al),
				"When": say.SpeakF(
					"actions.alarm.when",
					map[string]any{"Day": spokenDay(at.Weekday()), "Clock": at.Format("15:04")},
				),
			},
		), nil
	case "enable", "disable", "delete":
		al, reply := a.voiceTarget(c.String("target"))
		if reply != "" {
			return reply, nil
		}
		var err error
		if operation == "delete" {
			err = a.Delete(al.ID)
		} else {
			err = a.Enable(al.ID, operation == "enable")
		}
		if err != nil {
			return "", err
		}
		return say.SpeakF("actions.alarm."+operation, map[string]any{"Alarm": alarmLabel(al)}), nil
	case "stop":
		if !a.Stop() {
			return say.Speak("actions.alarm.not_ringing"), nil
		}
		return say.Speak("actions.alarm.stopped"), nil
	case "snooze":
		if !a.Snooze() {
			return say.Speak("actions.alarm.not_snoozed"), nil
		}
		return say.Speak("actions.alarm.snoozed"), nil
	default:
		return "", fmt.Errorf("unknown alarm operation %q", operation)
	}
}

func (a *Alarms) voiceTarget(target string) (Alarm, string) {
	target = strings.TrimSpace(target)
	if spokenNext(target) {
		if al, _, ok := a.Upcoming(); ok {
			return al, ""
		}
		return Alarm{}, say.Speak("actions.alarm.none")
	}
	if target == "" {
		return Alarm{}, say.Speak("actions.alarm.target")
	}
	hour, minute, clockErr := spokenClock(target)
	var matches []Alarm
	for _, al := range a.List() {
		if al.ID == target || strings.EqualFold(al.Name, target) ||
			(clockErr == nil && al.Hour == hour && al.Minute == minute) {
			matches = append(matches, al)
		}
	}
	switch len(matches) {
	case 0:
		return Alarm{}, say.Speak("actions.alarm.missing")
	case 1:
		return matches[0], ""
	default:
		return Alarm{}, say.Speak("actions.alarm.ambiguous")
	}
}

func alarmLabel(al Alarm) string {
	label := al.Clock()
	if al.Name != "" {
		label = say.SpeakF("actions.alarm.named", map[string]any{"Name": al.Name, "Clock": label})
	}
	if al.Once() {
		return label + ", " + say.Speak("actions.alarm.once")
	}
	return label + ", " + spokenDayNames(sorted(al.Days))
}
