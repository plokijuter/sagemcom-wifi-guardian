package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAuthKeyLikePython(t *testing.T) {
	// computed with the sagemcom_api 1.5.0 formula (hashlib.sha512)
	got := authKey("admin", "abc", sha512hex("mdp"), 3, 12345)
	want := "9b4b77364f48d80a5419a4d4e14d24538e7f74968fa108bc30ac9de93d1c2558098ec6e5e64cc2e95692d8556b94faabc5b31f9684f18c5f18e6d9096e1e6803"
	if got != want {
		t.Fatalf("key %s, want %s", got, want)
	}
}

func TestEscapeXPath(t *testing.T) {
	if g := escapeXPath("Device/WiFi/Radios/Radio[Alias='RADIO5G']/Channel"); g != "Device/WiFi/Radios/Radio[Alias='RADIO5G']/Channel" {
		t.Fatal(g)
	}
	if g := escapeXPath("a b"); g != "a%20b" {
		t.Fatal(g)
	}
}

func exampleRules(t *testing.T) []Rule {
	s, err := ParseSettings(exampleSettings)
	if err != nil {
		t.Fatal(err)
	}
	return s.Rules
}

func TestExampleSettings(t *testing.T) {
	r := exampleRules(t)
	var names []string
	for _, x := range r {
		names = append(names, x.Name)
	}
	if len(r) != 6 || !strings.HasSuffix(r[0].XPath, "/BlackListedChannels") || !strings.HasSuffix(r[2].XPath, "/OperatingChannelBandwidth") ||
		!strings.HasSuffix(r[3].XPath, "/Channel") || r[3].wait != 35*time.Minute {
		t.Fatalf("unexpected example rules: %v", names)
	}
	for _, ch := range strings.Split(r[0].Value, ",") {
		if ch >= "36" && ch <= "64" && len(ch) == 2 {
			t.Fatalf("channel %s blacklisted: 160 MHz would not apply", ch)
		}
	}
	if r[2].Value != "160MHz" {
		t.Fatal("width must be written 160MHz")
	}
	// the commented-out 6 GHz rule must parse once uncommented
	on := strings.NewReplacer("    // ,\n", "    ,\n", "    // { \"name\": \"6 GHz", "    { \"name\": \"6 GHz",
		"    //   \"", "    \"").Replace(string(exampleSettings))
	s, err := ParseSettings([]byte(on))
	if err != nil || len(s.Rules) != 7 || s.Rules[6].Suffix != "-6G" || s.Rules[6].CopyFrom == "" {
		t.Fatalf("6 GHz option: %v %+v", err, s)
	}
}

func TestParseSettingsErrors(t *testing.T) {
	ok := `{"rules":[{"name":"a","xpath":"x://y","value":"v // not a comment"}]} // trailing`
	s, err := ParseSettings([]byte(ok))
	if err != nil || s.Rules[0].Value != "v // not a comment" || s.Rules[0].XPath != "x://y" {
		t.Fatalf("comment stripping touched a string: %v %+v", err, s)
	}
	esc := `{"rules":[{"name":"a\"//b","xpath":"x","value":"v"}]}`
	if s, err := ParseSettings([]byte(esc)); err != nil || s.Rules[0].Name != `a"//b` {
		t.Fatalf("escaped quote: %v", err)
	}
	for _, bad := range []string{
		`{"rules":[]}`,
		`{"rules":[{"name":"a","xpath":"x","value":"v","typo":1}]}`,
		`{"rules":[{"name":"a","xpath":"x","value":"v"},{"name":"a","xpath":"y","value":"w"}]}`,
		`{"rules":[{"name":"a","xpath":"x"}]}`,
		`{"rules":[{"name":"a","xpath":"x","value":"v","copy_from":"z"}]}`,
		`{"rules":[{"name":"a","xpath":"x","value":"v","suffix":"-6G"}]}`,
		`{"rules":[{"name":"a","xpath":"x","value":"v","wait":"soon"}]}`,
		`{"rules":[{"xpath":"x","value":"v"}]}`,
	} {
		if _, err := ParseSettings([]byte(bad)); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
}

func TestEqual(t *testing.T) {
	r := exampleRules(t)
	if !equal(r[2], "160MHZ") || equal(r[2], "80MHz") {
		t.Fatal("width")
	}
	if !equal(r[4], "true") || equal(r[4], "false") {
		t.Fatal("boolean")
	}
	ssid := Rule{XPath: "Device/WiFi/SSIDs/SSID[Alias='WL_PRIV_6G']/SSID", Value: "Home-6G"}
	if equal(ssid, "home-6g") || !equal(ssid, "Home-6G") {
		t.Fatal("network names are case sensitive")
	}
}

func TestDecide(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	r := exampleRules(t)
	bf, ch := r[4], r[3]
	s := &Track{}
	if c, _ := Decide(bf, "False", s, t0); !c {
		t.Fatal("beamforming off: correct at once")
	}
	s.LastCorrected = t0
	if c, _ := Decide(bf, "False", s, t0.Add(30*time.Minute)); c {
		t.Fatal("not twice in an hour")
	}
	if c, _ := Decide(bf, "False", s, t0.Add(61*time.Minute)); !c {
		t.Fatal("after an hour, correct again")
	}
	sc := &Track{}
	if c, _ := Decide(ch, "100", sc, t0); c {
		t.Fatal("radar: do not go back to 44 at once")
	}
	if c, _ := Decide(ch, "100", sc, t0.Add(34*time.Minute)); c {
		t.Fatal("radar: wait 35 min")
	}
	if c, _ := Decide(ch, "100", sc, t0.Add(36*time.Minute)); !c {
		t.Fatal("after 35 min, back to 44")
	}
	if c, _ := Decide(ch, "44", sc, t0.Add(40*time.Minute)); c || !sc.DiffSince.IsZero() {
		t.Fatal("value back: the difference clock resets")
	}
}

func TestMaskSecrets(t *testing.T) {
	in := `[{"Alias":"PRIV0","Security":{"KeyPassphrase":"hunter2","SAEPassphrase":"x","PreSharedKey":"y",
		"RadiusSecret":"z","ModeEnabled":"WPA2_PERSONAL","RekeyingInterval":3600},
		"WPS":{"DevicePIN":"12345670","EnrolleePIN":""},"RadioMeasurements":{"BeaconRequestPassive":true},
		"Accounting":{"Secret":"s"},"SSID":"Home"}]`
	out := prettyMasked(json.RawMessage(in))
	for _, leak := range []string{"hunter2", "12345670", `"y"`, `"z"`, `"s"`} {
		if strings.Contains(out, leak) {
			t.Errorf("secret %s leaked:\n%s", leak, out)
		}
	}
	for _, keep := range []string{"WPA2_PERSONAL", "3600", `"BeaconRequestPassive": true`, `"Home"`, `"EnrolleePIN": ""`} {
		if !strings.Contains(out, keep) {
			t.Errorf("%s should stay visible:\n%s", keep, out)
		}
	}
}

// fakeRouter speaks just enough XMO for the tests.
type fakeRouter struct {
	mu      sync.Mutex
	values  map[string]any
	methods []string
	sets    []string
	status  int
}

func (f *fakeRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	var req struct {
		Request struct {
			Actions []struct {
				Method     string
				XPath      string `json:"xpath"`
				Parameters map[string]any
			}
		}
	}
	json.Unmarshal([]byte(r.FormValue("req")), &req)
	var acts []any
	for _, a := range req.Request.Actions {
		f.methods = append(f.methods, a.Method)
		var params any = map[string]any{}
		switch a.Method {
		case "logIn":
			params = map[string]any{"id": 42, "nonce": "n0nce"}
		case "getValue":
			params = map[string]any{"value": f.values[a.XPath]}
		case "setValue":
			f.values[a.XPath] = a.Parameters["value"]
			f.sets = append(f.sets, a.XPath)
		}
		acts = append(acts, map[string]any{"error": map[string]string{"description": "XMO_NO_ERR"},
			"callbacks": []any{map[string]any{"parameters": params}}})
	}
	json.NewEncoder(w).Encode(map[string]any{"reply": map[string]any{
		"error": map[string]string{"description": "XMO_REQUEST_NO_ERR"}, "actions": acts}})
}

func newFake(t *testing.T, values map[string]any) (*fakeRouter, Config) {
	f := &fakeRouter{values: values}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, Config{Host: strings.TrimPrefix(srv.URL, "http://"), User: "admin", Password: "pw"}
}

func TestOnlyWhitelistedMethodsAreSent(t *testing.T) {
	f, cfg := newFake(t, map[string]any{})
	c := NewClient(cfg.Host, cfg.User, cfg.Password)
	for _, m := range []string{"getDownloadURI", "getVendorLogDownloadURI", "reboot", "restoreFrom", ""} {
		_, err := c.request([]map[string]any{{"id": 0, "method": "getValue"}, {"id": 1, "method": m}}, false)
		if err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("%q not refused: %v", m, err)
		}
	}
	if len(f.methods) != 0 {
		t.Fatalf("something reached the router: %v", f.methods)
	}
	if err := c.Login(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get([]string{"Device/DeviceInfo/UpTime"}); err != nil {
		t.Fatal(err)
	}
	c.Logout()
	if strings.Join(f.methods, ",") != "logIn,getValue,logOut" {
		t.Fatal(f.methods)
	}
}

func TestHTTP5xxStopsEverything(t *testing.T) {
	f, cfg := newFake(t, map[string]any{})
	c := NewClient(cfg.Host, cfg.User, cfg.Password)
	if err := c.Login(); err != nil {
		t.Fatal(err)
	}
	f.status = 503
	if _, err := c.Get([]string{"x"}); !errors.Is(err, ErrRouterDistress) {
		t.Fatalf("503: %v", err)
	}
	f.status = 0
	n := len(f.methods)
	c.Logout()
	if _, err := c.Get([]string{"x"}); !errors.Is(err, ErrRouterDistress) || len(f.methods) != n {
		t.Fatal("after a 5xx nothing more must be sent, not even logOut")
	}
}

func TestRoundCorrectsInOrder(t *testing.T) {
	pauseAfterSet = 0
	r5 := "Device/WiFi/Radios/Radio[Alias='RADIO5G']/"
	f, cfg := newFake(t, map[string]any{
		"Device/DeviceInfo/UpTime":                                 1234567, // > 1e6: must not turn into 1.234567e+06
		r5 + "BlackListedChannels":                                 "36,40,48,52,56,64,104,108,112,116,136,140,144,149,153,161,165",
		r5 + "AutoChannelEnable":                                   true,
		r5 + "OperatingChannelBandwidth":                           "80MHz",
		r5 + "Channel":                                             44,
		r5 + "TransmitBeamForming":                                 true,
		"Device/WiFi/Radios/Radio[Alias='RADIO6G']/GreenAPEnabled": true,
		"Device/WiFi/SSIDs/SSID[Alias='WL_PRIV']/SSID":             "Home",
		"Device/WiFi/SSIDs/SSID[Alias='WL_PRIV_6G']/SSID":          "Home",
	})
	s, _ := ParseSettings(exampleSettings)
	s.Rules = append(s.Rules, Rule{Name: "6 GHz network name", XPath: "Device/WiFi/SSIDs/SSID[Alias='WL_PRIV_6G']/SSID",
		CopyFrom: "Device/WiFi/SSIDs/SSID[Alias='WL_PRIV']/SSID", Suffix: "-6G"})
	tracks := map[string]*Track{}

	_, msgs, err := round(cfg, s, tracks, true)
	if err != nil || len(f.sets) != 0 || len(msgs) != 5 {
		t.Fatalf("dry run must only report: %v %v %v", err, f.sets, msgs)
	}
	_, msgs, err = round(cfg, s, tracks, false)
	if err != nil {
		t.Fatal(err, msgs)
	}
	want := []string{r5 + "BlackListedChannels", r5 + "AutoChannelEnable", r5 + "OperatingChannelBandwidth",
		"Device/WiFi/Radios/Radio[Alias='RADIO6G']/GreenAPEnabled", "Device/WiFi/SSIDs/SSID[Alias='WL_PRIV_6G']/SSID"}
	if strings.Join(f.sets, "|") != strings.Join(want, "|") {
		t.Fatalf("writes %v\nwant  %v", f.sets, want)
	}
	if f.values["Device/WiFi/SSIDs/SSID[Alias='WL_PRIV_6G']/SSID"] != "Home-6G" || f.values[r5+"OperatingChannelBandwidth"] != "160MHz" {
		t.Fatal(f.values)
	}
	f.sets = nil
	if _, msgs, _ = round(cfg, s, tracks, false); len(f.sets) != 0 || len(msgs) != 0 {
		t.Fatalf("all in place: nothing to do, got %v %v", f.sets, msgs)
	}

	f.values["Device/DeviceInfo/UpTime"] = 120
	f.values[r5+"TransmitBeamForming"] = false
	if _, msgs, _ = round(cfg, s, tracks, false); len(f.sets) != 0 || len(msgs) != 1 {
		t.Fatalf("router just booted: wait, got %v %v", f.sets, msgs)
	}
}
