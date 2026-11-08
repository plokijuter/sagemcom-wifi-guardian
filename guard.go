package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

var pauseAfterSet = 5 * time.Second // let the radio apply a change before the next one

//go:embed settings.example.jsonc
var exampleSettings []byte

const (
	minUptime      = 5 * time.Minute  // a router that just booted is still setting itself up: do not fight it
	distressPause  = 15 * time.Minute // after an HTTP 5xx, leave the router alone for a while
	guardLoginWait = 20 * time.Second
)

func cmdGuard(cfgPath, settingsPath, logPath string, interval time.Duration, once, dry bool) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	dir := filepath.Dir(cfgPath)
	if settingsPath == "" {
		settingsPath = filepath.Join(dir, "settings.json")
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("%v\n(run `init`, or copy settings.example.jsonc there and edit it)", err)
	}
	s, err := ParseSettings(data)
	if err != nil {
		return err
	}
	if logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		log.SetOutput(f)
	}
	statePath := filepath.Join(dir, "state.json")
	tracks := map[string]*Track{}
	if b, err := os.ReadFile(statePath); err == nil {
		var st struct {
			Tracks map[string]*Track `json:"tracks"`
		}
		if json.Unmarshal(b, &st) == nil && st.Tracks != nil {
			tracks = st.Tracks
		}
	}
	log.Printf("start (dry-run=%v, interval %v, %d rules from %s)", dry, interval, len(s.Rules), settingsPath)
	lastOK := time.Time{}
	for {
		values, msgs, err := round(cfg, s, tracks, dry)
		st := map[string]any{"at": time.Now().Format(time.RFC3339), "tracks": tracks, "values": values}
		if err != nil {
			st["error"] = err.Error()
			log.Printf("ERROR %v", err)
		}
		for _, m := range msgs {
			log.Print(m)
		}
		if err == nil && len(msgs) == 0 && (once || time.Since(lastOK) > time.Hour) {
			log.Print("OK all settings in place") // one sign of life per hour
			lastOK = time.Now()
		}
		if b, e := json.MarshalIndent(st, "", " "); e == nil {
			os.WriteFile(statePath, b, 0o644)
		}
		if once {
			return err
		}
		wait := interval
		if errors.Is(err, ErrRouterDistress) && wait < distressPause {
			wait = distressPause
		}
		time.Sleep(wait)
	}
}

// round: one short admin session (the router accepts only one), read, correct, log out.
func round(cfg Config, s *Settings, tracks map[string]*Track, dry bool) (map[string]string, []string, error) {
	c, err := connect(cfg, 3, guardLoginWait)
	if err != nil {
		return nil, nil, err
	}
	defer c.Logout()

	// copy_from rules (e.g. 6 GHz name = main name + "-6G") are computed once, at the first round.
	for i := range s.Rules {
		r := &s.Rules[i]
		if r.CopyFrom != "" && r.Value == "" {
			v, err := c.Get([]string{r.CopyFrom})
			if err != nil {
				return nil, nil, fmt.Errorf("reading %s for %q: %w", r.CopyFrom, r.Name, err)
			}
			if v[0] == "" {
				return nil, nil, fmt.Errorf("%s is empty, cannot build %q", r.CopyFrom, r.Name)
			}
			r.Value = v[0] + r.Suffix
		}
	}

	xp := []string{"Device/DeviceInfo/UpTime"}
	for _, r := range s.Rules {
		xp = append(xp, r.XPath)
	}
	v, err := c.Get(xp)
	if err != nil {
		return nil, nil, fmt.Errorf("reading (check the xpaths of the settings file with `get`): %w", err)
	}
	values := map[string]string{"uptime_s": v[0]}
	uptime, err := strconv.Atoi(v[0])
	if err != nil {
		return values, nil, fmt.Errorf("unexpected uptime %q", v[0])
	}
	var msgs []string
	now := time.Now()
	for i, r := range s.Rules {
		got := v[i+1]
		values[r.Name] = got
		t := tracks[r.Name]
		if t == nil {
			t = &Track{}
			tracks[r.Name] = t
		}
		correct, reason := Decide(r, got, t, now)
		if equal(r, got) {
			continue
		}
		switch {
		case dry:
			msgs = append(msgs, fmt.Sprintf("DIFF %s: %q (wanted %q) %s", r.Name, got, r.Value, reason))
		case time.Duration(uptime)*time.Second < minUptime:
			msgs = append(msgs, fmt.Sprintf("DIFF %s: %q, router up for only %d s, waiting", r.Name, got, uptime))
		case !correct:
			msgs = append(msgs, fmt.Sprintf("DIFF %s: %q (wanted %q), %s", r.Name, got, r.Value, reason))
		default:
			t.LastCorrected = now
			if e := c.Set(r.XPath, r.Value); e != nil {
				msgs = append(msgs, fmt.Sprintf("ERROR correcting %s: %v", r.Name, e))
				if errors.Is(e, ErrRouterDistress) {
					return values, msgs, e // the router may be crashing: stop this round now
				}
				continue
			}
			msgs = append(msgs, fmt.Sprintf("CORRECTED %s: %q -> %q", r.Name, got, r.Value))
			time.Sleep(pauseAfterSet)
		}
	}
	return values, msgs, nil
}
