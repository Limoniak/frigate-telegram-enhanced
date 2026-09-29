// Package actions encode et décode les callback_data des boutons Telegram (64 octets max).
package actions

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	KindMute     = "m" // m:<caméra>:<secondes>
	KindPause    = "p" // p:<secondes>
	KindClip     = "c" // c:<id événement ou review>
	KindSnapshot = "s" // s:<caméra>
	KindUnmute   = "u" // u:<caméra>
	KindResume   = "r" // r:all
	KindRefresh  = "f" // f:menu

	// menuPrefix marque un bouton du menu de contrôle : après l'action, le message du
	// menu est redessiné pour refléter le nouvel état.
	menuPrefix = "!"
)

type Action struct {
	Kind     string
	Camera   string
	ID       string
	Duration time.Duration
	Menu     bool // bouton du menu de contrôle
}

func Unmute(camera string) string { return KindUnmute + ":" + camera }

func Resume() string { return KindResume + ":all" }

func Refresh() string { return KindRefresh + ":menu" }

// InMenu marque data comme un bouton du menu de contrôle.
func InMenu(data string) string { return menuPrefix + data }

func Mute(camera string, d time.Duration) string {
	return KindMute + ":" + camera + ":" + strconv.Itoa(int(d.Seconds()))
}

func Pause(d time.Duration) string { return KindPause + ":" + strconv.Itoa(int(d.Seconds())) }

func Clip(id string) string { return KindClip + ":" + id }

func Snapshot(camera string) string { return KindSnapshot + ":" + camera }

func Parse(data string) (Action, error) {
	invalid := fmt.Errorf("invalid action %q", data)
	body, menu := strings.CutPrefix(data, menuPrefix)
	a, err := parse(body)
	if err != nil {
		return Action{}, invalid
	}
	a.Menu = menu
	return a, nil
}

func parse(data string) (Action, error) {
	invalid := fmt.Errorf("invalid action %q", data)
	kind, rest, ok := strings.Cut(data, ":")
	if !ok || rest == "" {
		return Action{}, invalid
	}
	seconds := func(s string) (time.Duration, bool) {
		n, err := strconv.Atoi(s)
		return time.Duration(n) * time.Second, err == nil && n > 0
	}
	switch kind {
	case KindMute:
		i := strings.LastIndexByte(rest, ':')
		if i <= 0 {
			return Action{}, invalid
		}
		d, ok := seconds(rest[i+1:])
		if !ok {
			return Action{}, invalid
		}
		return Action{Kind: kind, Camera: rest[:i], Duration: d}, nil
	case KindPause:
		d, ok := seconds(rest)
		if !ok {
			return Action{}, invalid
		}
		return Action{Kind: kind, Duration: d}, nil
	case KindClip:
		return Action{Kind: kind, ID: rest}, nil
	case KindSnapshot, KindUnmute:
		return Action{Kind: kind, Camera: rest}, nil
	case KindResume, KindRefresh:
		return Action{Kind: kind}, nil
	}
	return Action{}, invalid
}
