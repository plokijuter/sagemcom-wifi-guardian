package main

// Minimal client for the "XMO" JSON API of Sagemcom routers (F@st 5690 / Bell Giga Hub),
// ported from the Python library sagemcom_api 1.5.0 (SHA-512 authentication).

import (
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const apiPath = "/cgi/json-req"

// allowedMethods is a whitelist. Anything else is refused before a byte is sent:
// getDownloadURI crashed a Giga Hub three times in one morning (HTTP 500, then 503,
// then a watchdog reset and about 12 minutes without Internet or Wi-Fi each time).
var allowedMethods = map[string]bool{"logIn": true, "logOut": true, "getValue": true, "setValue": true}

// ErrRouterDistress marks HTTP 5xx answers: the router may be about to crash, stop talking to it.
var ErrRouterDistress = errors.New("router answered HTTP 5xx, it may be in trouble: stopping now")

type Client struct {
	host, user, passHash string
	session              int64
	serverNonce          string
	requestID            int
	distress             bool
	http                 *http.Client
}

func NewClient(host, user, password string) *Client {
	return &Client{host: host, user: user, passHash: sha512hex(password), requestID: -1,
		http: &http.Client{Timeout: 20 * time.Second}}
}

func sha512hex(s string) string { h := sha512.Sum512([]byte(s)); return hex.EncodeToString(h[:]) }

// authKey reproduces __generate_auth_key:
// sha512(sha512(user:serverNonce:sha512(password)):id:cnonce:JSON:/cgi/json-req)
func authKey(user, serverNonce, passHash string, id int, cnonce int) string {
	cred := sha512hex(user + ":" + serverNonce + ":" + passHash)
	return sha512hex(fmt.Sprintf("%s:%d:%d:JSON:%s", cred, id, cnonce, apiPath))
}

type xmoError struct {
	Description string `json:"description"`
}

type response struct {
	Reply struct {
		Error   xmoError `json:"error"`
		Actions []struct {
			Error     xmoError `json:"error"`
			Callbacks []struct {
				Parameters json.RawMessage `json:"parameters"`
			} `json:"callbacks"`
		} `json:"actions"`
	} `json:"reply"`
}

func (c *Client) request(actions []map[string]any, priority bool) (*response, error) {
	for _, a := range actions {
		if m, _ := a["method"].(string); !allowedMethods[m] {
			return nil, fmt.Errorf("method %q refused: only logIn, logOut, getValue and setValue are allowed", m)
		}
	}
	if c.distress {
		return nil, ErrRouterDistress
	}
	c.requestID++
	cnonce := rand.Intn(500000)
	body, _ := json.Marshal(map[string]any{"request": map[string]any{
		"id": c.requestID, "session-id": c.session, "priority": priority, "actions": actions,
		"cnonce": cnonce, "auth-key": authKey(c.user, c.serverNonce, c.passHash, c.requestID, cnonce),
	}})
	r, err := c.http.PostForm("http://"+c.host+apiPath, url.Values{"req": {string(body)}})
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if r.StatusCode >= 500 {
		c.distress = true
		return nil, fmt.Errorf("HTTP %d: %w", r.StatusCode, ErrRouterDistress)
	}
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", r.StatusCode)
	}
	var rep response
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("unreadable answer: %v", err)
	}
	if d := rep.Reply.Error.Description; d != "XMO_REQUEST_NO_ERR" && d != "Ok" {
		for _, a := range rep.Reply.Actions {
			if a.Error.Description != "XMO_NO_ERR" {
				return nil, fmt.Errorf("%s / %s", d, a.Error.Description)
			}
		}
		return nil, fmt.Errorf("%s", d)
	}
	return &rep, nil
}

func (c *Client) Login() error {
	rep, err := c.request([]map[string]any{{"id": 0, "method": "logIn", "parameters": map[string]any{
		"user": c.user, "persistent": true,
		"session-options": map[string]any{
			"nss":              []map[string]string{{"name": "gtw", "uri": "http://sagemcom.com/gateway-data"}},
			"language":         "ident",
			"context-flags":    map[string]bool{"get-content-name": true, "local-time": true},
			"capability-depth": 2,
			"capability-flags": map[string]bool{"name": true, "default-value": false, "restriction": true, "description": false},
			"time-format":      "ISO_8601", "write-only-string": "_XMO_WRITE_ONLY_",
			"undefined-write-only-string": "_XMO_UNDEFINED_WRITE_ONLY_",
		}}}}, true)
	if err != nil {
		return err
	}
	var p struct {
		ID    int64  `json:"id"`
		Nonce string `json:"nonce"`
	}
	if len(rep.Reply.Actions) == 0 || len(rep.Reply.Actions[0].Callbacks) == 0 ||
		json.Unmarshal(rep.Reply.Actions[0].Callbacks[0].Parameters, &p) != nil || p.Nonce == "" {
		return fmt.Errorf("login refused")
	}
	c.session, c.serverNonce = p.ID, p.Nonce
	return nil
}

// Logout frees the router's only admin session. Skipped after a 5xx: do not poke a router in trouble.
func (c *Client) Logout() {
	if !c.distress {
		c.request([]map[string]any{{"id": 0, "method": "logOut"}}, false)
	}
	c.session, c.serverNonce, c.requestID = -1, "", -1
}

func escapeXPath(x string) string {
	// same as urllib.parse.quote(xpath, "/=[]'@\"")
	var b strings.Builder
	for _, ch := range []byte(x) {
		if ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || strings.IndexByte("_.-~/=[]'@\"", ch) >= 0 {
			b.WriteByte(ch)
		} else {
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

// GetRaw returns the raw JSON value of each xpath (a scalar or a whole subtree), in one request.
func (c *Client) GetRaw(xpaths []string) ([]json.RawMessage, error) {
	var actions []map[string]any
	for i, x := range xpaths {
		actions = append(actions, map[string]any{"id": i, "method": "getValue", "xpath": escapeXPath(x), "options": map[string]any{}})
	}
	rep, err := c.request(actions, false)
	if err != nil {
		return nil, err
	}
	out := make([]json.RawMessage, len(xpaths))
	for i := range xpaths {
		if i >= len(rep.Reply.Actions) || len(rep.Reply.Actions[i].Callbacks) == 0 {
			return nil, fmt.Errorf("no value for %s", xpaths[i])
		}
		var p struct {
			Value json.RawMessage `json:"value"`
		}
		json.Unmarshal(rep.Reply.Actions[i].Callbacks[0].Parameters, &p)
		out[i] = p.Value
	}
	return out, nil
}

// Get returns each value as text: strings unquoted, numbers and booleans as sent
// (never through float64, which would print a long uptime as 1.234567e+06).
func (c *Client) Get(xpaths []string) ([]string, error) {
	raw, err := c.GetRaw(xpaths)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(raw))
	for i, r := range raw {
		out[i] = scalar(r)
	}
	return out, nil
}

func scalar(r json.RawMessage) string {
	var s string
	if json.Unmarshal(r, &s) == nil {
		return s
	}
	return string(r)
}

// Set sends the value as text, like sagemcom_api (str(value)): "True", "160MHz".
func (c *Client) Set(xpath, value string) error {
	_, err := c.request([]map[string]any{{"id": 0, "method": "setValue", "xpath": escapeXPath(xpath),
		"parameters": map[string]string{"value": value}, "options": map[string]any{}}}, false)
	return err
}
