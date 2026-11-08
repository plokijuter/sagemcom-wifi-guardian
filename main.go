// sagemcom-wifi-guardian: read and change the hidden Wi-Fi settings of Sagemcom F@st 5690
// routers (Bell Giga Hub and others), and keep them in place when the router reverts them.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const usage = `sagemcom-wifi-guardian: hidden Wi-Fi settings of Sagemcom F@st 5690 routers (Bell Giga Hub).

Usage:
  sagemcom-wifi-guardian init                       ask for address, user and password, test them, save them
  sagemcom-wifi-guardian get <xpath>...             read values
  sagemcom-wifi-guardian set <xpath> <value>        write one value (as text, e.g. True, 160MHz)
  sagemcom-wifi-guardian dump [<xpath>]             read a subtree as JSON, secrets masked (default Device/WiFi)
  sagemcom-wifi-guardian options                    catalogue of useful hidden settings, with current values
  sagemcom-wifi-guardian clients                    Wi-Fi clients and link rates (the real proof of 160 MHz)
  sagemcom-wifi-guardian guard [--once] [--dry-run] keep the settings of settings.json in place

Every command accepts, before its arguments:
  --config <file>   credentials file (default: %s)

Environment variables override the file: SAGEMCOM_HOST, SAGEMCOM_USER, SAGEMCOM_PASSWORD.
The password is never taken from the command line (it would show in the process list).

Only the logIn, logOut, getValue and setValue API methods are ever sent: getDownloadURI is known
to crash this router. On HTTP 500/503 the tool stops at once and does not even log out.
The router accepts ONE admin session at a time: close its web page, and expect short waits.
`

type Config struct {
	Host     string `json:"host"`
	User     string `json:"user"`
	Password string `json:"password"`
}

func defaultConfigPath() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	return filepath.Join(d, "sagemcom-wifi-guardian", "config.json")
}

func loadConfig(path string) (Config, error) {
	c := Config{Host: "192.168.2.1", User: "admin"}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("%s: %v", path, err)
		}
		if fi, e := os.Stat(path); e == nil && runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
			fmt.Fprintf(os.Stderr, "warning: %s is readable by others, run: chmod 600 %s\n", path, path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	for env, dst := range map[string]*string{"SAGEMCOM_HOST": &c.Host, "SAGEMCOM_USER": &c.User, "SAGEMCOM_PASSWORD": &c.Password} {
		if v := os.Getenv(env); v != "" {
			*dst = v
		}
	}
	if c.Password == "" {
		return c, fmt.Errorf("no password: run `sagemcom-wifi-guardian init` or set SAGEMCOM_PASSWORD")
	}
	return c, nil
}

// connect logs in, retrying: the router accepts a single admin session and another tool
// (web page, phone app, a guard) may hold it for a few seconds.
func connect(c Config, tries int, pause time.Duration) (*Client, error) {
	cl := NewClient(c.Host, c.User, c.Password)
	var err error
	for i := 0; i < tries; i++ {
		if i > 0 {
			fmt.Fprintf(os.Stderr, "login failed (%v), retrying in %v (one admin session at a time)\n", err, pause)
			time.Sleep(pause)
		}
		if err = cl.Login(); err == nil || errors.Is(err, ErrRouterDistress) {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("login to %s: %w", c.Host, err)
	}
	return cl, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, defaultConfigPath())
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, usage, defaultConfigPath()) }
	cfgPath := fs.String("config", defaultConfigPath(), "credentials file")
	once := fs.Bool("once", false, "guard: one round, then exit")
	dry := fs.Bool("dry-run", false, "guard: read and report, never write")
	settingsPath := fs.String("settings", "", "guard: settings file (default: settings.json next to the config file)")
	interval := fs.Duration("interval", 5*time.Minute, "guard: time between two rounds")
	logPath := fs.String("log", "", "guard: append the log to this file instead of standard error")
	fs.Parse(os.Args[2:])
	args := fs.Args()

	var err error
	switch cmd {
	case "init":
		err = cmdInit(*cfgPath)
	case "options":
		err = cmdOptions(*cfgPath)
	case "get", "set", "dump", "clients":
		err = withRouter(*cfgPath, func(c *Client) error { return runSimple(c, cmd, args) })
	case "guard":
		err = cmdGuard(*cfgPath, *settingsPath, *logPath, *interval, *once, *dry)
	case "help", "-h", "--help":
		fs.Usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		fs.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func withRouter(cfgPath string, f func(*Client) error) error {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	c, err := connect(cfg, 3, 10*time.Second)
	if err != nil {
		return err
	}
	defer c.Logout()
	return f(c)
}

func runSimple(c *Client, cmd string, args []string) error {
	switch cmd {
	case "get":
		if len(args) == 0 {
			return fmt.Errorf("usage: get <xpath>...")
		}
		raw, err := c.GetRaw(args)
		if err != nil {
			return err
		}
		for i, r := range raw {
			v := scalar(r)
			if len(r) > 0 && (r[0] == '{' || r[0] == '[') {
				v = prettyMasked(r)
			}
			if len(args) > 1 {
				fmt.Printf("%s = ", args[i])
			}
			fmt.Println(v)
		}
	case "set":
		if len(args) != 2 {
			return fmt.Errorf("usage: set <xpath> <value>")
		}
		return cmdSet(c, args[0], args[1])
	case "dump":
		x := "Device/WiFi"
		if len(args) > 0 {
			x = args[0]
		}
		raw, err := c.GetRaw([]string{x})
		if err != nil {
			return err
		}
		fmt.Println(prettyMasked(raw[0]))
	case "clients":
		return cmdClients(c)
	}
	return nil
}

func cmdSet(c *Client, xpath, value string) error {
	old, err := c.Get([]string{xpath})
	if err != nil {
		return err
	}
	if err := c.Set(xpath, value); err != nil {
		if strings.Contains(err.Error(), "XMO_ENUM_ERR") {
			err = fmt.Errorf("%w\nhint: the value is not in the router's list. Widths need the unit (160MHz, not 160); see `options`", err)
		}
		return err
	}
	now, err := c.Get([]string{xpath})
	if err != nil {
		return err
	}
	fmt.Printf("%s: %q -> %q\n", xpath, old[0], now[0])
	if strings.HasSuffix(xpath, "/OperatingChannelBandwidth") {
		fmt.Println("note: CurrentOperatingChannelBandwidth may keep saying 80MHz even at 160 MHz; check link rates with `clients`.")
	}
	return nil
}

// isSecretKey: field names that hold passwords, keys or PINs.
func isSecretKey(k string) bool {
	l := strings.ToLower(k)
	for _, w := range []string{"password", "passphrase", "secret", "psk", "token", "credential"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return strings.HasSuffix(l, "key") || strings.HasSuffix(l, "pin")
}

func maskSecrets(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if s, ok := x.(string); ok && s != "" && isSecretKey(k) {
				t[k] = "***"
			} else {
				t[k] = maskSecrets(x)
			}
		}
	case []any:
		for i := range t {
			t[i] = maskSecrets(t[i])
		}
	}
	return v
}

func prettyMasked(raw json.RawMessage) string {
	var v any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return string(raw)
	}
	b, _ := json.MarshalIndent(maskSecrets(v), "", "  ")
	return string(b)
}

var stdin = bufio.NewReader(os.Stdin)

func readLine() (string, error) {
	s, err := stdin.ReadString('\n')
	if err != nil && s == "" {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

func ask(prompt, def string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s [%s]: ", prompt, def)
	s, err := readLine()
	if s == "" {
		s = def
	}
	return s, err
}

func cmdInit(cfgPath string) error {
	cfg := Config{Host: "192.168.2.1", User: "admin"}
	if b, err := os.ReadFile(cfgPath); err == nil {
		json.Unmarshal(b, &cfg)
	}
	var err error
	if cfg.Host, err = ask("Router address", cfg.Host); err != nil {
		return err
	}
	if cfg.User, err = ask("Admin user", cfg.User); err != nil {
		return err
	}
	fmt.Fprint(os.Stderr, "Admin password (not shown): ")
	if cfg.Password, err = readSecret(); err != nil || cfg.Password == "" {
		return fmt.Errorf("no password given")
	}
	c, err := connect(cfg, 1, 0)
	if err == nil {
		v, e := c.Get([]string{"Device/DeviceInfo/ModelName", "Device/DeviceInfo/SoftwareVersion"})
		c.Logout()
		if e == nil {
			fmt.Fprintf(os.Stderr, "connected: %s, firmware %s\n", v[0], v[1])
		}
	} else {
		fmt.Fprintf(os.Stderr, "test failed: %v\nSave anyway? [y/N]: ", err)
		if a, _ := readLine(); !strings.EqualFold(a, "y") {
			return fmt.Errorf("nothing saved")
		}
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		return err
	}
	os.Chmod(cfgPath, 0o600) // WriteFile keeps the mode of an existing file
	fmt.Fprintf(os.Stderr, "saved %s\n", cfgPath)
	if runtime.GOOS == "windows" {
		// Windows ignores the 0600 mode: only SYSTEM, Administrators and you should read the password.
		fmt.Fprintf(os.Stderr, "restrict it (see README):\n  icacls \"%s\" /inheritance:r /grant:r \"%s:F\" \"*S-1-5-18:F\" \"*S-1-5-32-544:F\"\n",
			cfgPath, os.Getenv("USERNAME"))
	}
	sp := filepath.Join(filepath.Dir(cfgPath), "settings.json")
	if _, err := os.Stat(sp); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(sp, exampleSettings, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "wrote the example settings %s: read it and edit it before `guard`\n", sp)
	}
	return nil
}
