package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Rule is one wanted setting. Order matters and is kept: on the 5 GHz radio, the channel
// blacklist must come before the width, and the width before the channel.
type Rule struct {
	Name  string `json:"name"`
	XPath string `json:"xpath"`
	Value string `json:"value"`
	// Wait: how long a difference must last before it is corrected (e.g. "35m" for a DFS channel).
	Wait string `json:"wait,omitempty"`
	// CopyFrom + Suffix: the wanted value is read once from another xpath at start-up, plus a suffix
	// (used for "6 GHz name = main name + -6G").
	CopyFrom string `json:"copy_from,omitempty"`
	Suffix   string `json:"suffix,omitempty"`

	wait time.Duration
}

type Settings struct {
	Rules []Rule `json:"rules"`
}

// ParseSettings reads the settings file: JSON, with // comments allowed.
func ParseSettings(data []byte) (*Settings, error) {
	var s Settings
	dec := json.NewDecoder(strings.NewReader(stripComments(string(data))))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("settings: %v", err)
	}
	if len(s.Rules) == 0 {
		return nil, fmt.Errorf("settings: no rules")
	}
	seen := map[string]bool{}
	for i := range s.Rules {
		r := &s.Rules[i]
		switch {
		case r.Name == "" || r.XPath == "":
			return nil, fmt.Errorf("settings: rule %d needs a name and an xpath", i+1)
		case seen[r.Name]:
			return nil, fmt.Errorf("settings: two rules are named %q", r.Name)
		case (r.Value == "") == (r.CopyFrom == ""):
			return nil, fmt.Errorf("settings: rule %q needs either value or copy_from", r.Name)
		case r.Suffix != "" && r.CopyFrom == "":
			return nil, fmt.Errorf("settings: rule %q has a suffix without copy_from", r.Name)
		}
		seen[r.Name] = true
		if r.Wait != "" {
			d, err := time.ParseDuration(r.Wait)
			if err != nil || d < 0 {
				return nil, fmt.Errorf("settings: rule %q: bad wait %q (examples: 35m, 1h)", r.Name, r.Wait)
			}
			r.wait = d
		}
	}
	return &s, nil
}

// stripComments removes // comments that are outside JSON strings.
func stripComments(s string) string {
	var b strings.Builder
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inStr:
			if esc {
				esc = false
			} else if ch == '\\' {
				esc = true
			} else if ch == '"' {
				inStr = false
			}
		case ch == '"':
			inStr = true
		case ch == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			if i < len(s) {
				b.WriteByte('\n')
			}
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

// equal: network names (SSID) compare exactly; everything else ignores case and spaces,
// because the router may answer "160MHZ" for "160MHz" or "true" for "True".
func equal(r Rule, got string) bool {
	if strings.HasSuffix(r.XPath, "/SSID") {
		return got == r.Value
	}
	n := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, " ", "")) }
	return n(got) == n(r.Value)
}

// Track: since when a difference exists, and when it was last corrected.
type Track struct {
	DiffSince     time.Time `json:"diff_since,omitempty"`
	LastCorrected time.Time `json:"last_corrected,omitempty"`
}

const correctionSpacing = time.Hour // at most one correction per setting per hour

// Decide says whether to correct now, and updates the track.
func Decide(r Rule, got string, t *Track, now time.Time) (correct bool, reason string) {
	if equal(r, got) {
		t.DiffSince = time.Time{}
		return false, ""
	}
	if t.DiffSince.IsZero() {
		t.DiffSince = now
	}
	if left := r.wait - now.Sub(t.DiffSince); left > 0 {
		return false, "waiting " + left.Round(time.Minute).String()
	}
	if !t.LastCorrected.IsZero() && now.Sub(t.LastCorrected) < correctionSpacing {
		return false, "already corrected " + now.Sub(t.LastCorrected).Round(time.Minute).String() + " ago"
	}
	return true, ""
}
