package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// Catalogue of useful settings that the router's web page does not show.
// Sources: the public list of paths of this model, and reads made on a F@st 5690 (firmware 2.14.x).
type option struct {
	field, title, desc, values, risk string
	scope                            string   // "radio", "ap" or "ssid"
	bands                            []string // radio options: limit to these bands (empty = all)
	valuesFrom                       string   // sibling field where the router lists the accepted values
}

var radios = []struct{ band, alias string }{{"2.4", "RADIO2G4"}, {"5", "RADIO5G"}, {"6", "RADIO6G"}}

var catalog = []option{
	{field: "TransmitBeamForming", scope: "radio", title: "Beamforming",
		desc: "Explicit beamforming: aims the signal at each client. Was off on 5 GHz by default on our unit.", values: "True, False"},
	{field: "DownlinkOFDMAEnable", scope: "radio", title: "OFDMA, downlink",
		desc: "Wi-Fi 6 OFDMA towards clients (several small clients served in one transmission).", values: "True, False"},
	{field: "UplinkOFDMAEnable", scope: "radio", title: "OFDMA, uplink",
		desc: "Wi-Fi 6 OFDMA from clients.", values: "True, False", risk: "some older Wi-Fi 6 clients behave badly with uplink OFDMA"},
	{field: "DLMUMIMOEnabled", scope: "radio", title: "MU-MIMO, downlink",
		desc: "Sends to several clients at once on separate spatial streams.", values: "True, False"},
	{field: "ATFEnable", scope: "radio", title: "Airtime fairness (ATF)",
		desc: "Shares air time instead of packets, so one slow client cannot hog the radio.", values: "True, False"},
	{field: "GreenAPEnabled", scope: "radio", title: "GreenAP power saving",
		desc: "Turns radio chains off when traffic is low (delay: GreenAPDelay). On by default on 6 GHz on our unit.", values: "True, False",
		risk: "when on, the first packets after a quiet period can be slower"},
	{field: "CSAEnable", scope: "radio", title: "Channel switch announcement (CSA)",
		desc: "Warns clients before the radio changes channel (radar, auto channel), so they follow instead of dropping.", values: "True, False"},
	{field: "OperatingChannelBandwidth", scope: "radio", title: "Channel width", valuesFrom: "SupportedChannelBandwidth",
		desc:   "Width the radio is set to. The router lists 20,40,AUTO,80,160 but refuses \"160\" (XMO_ENUM_ERR): write the unit, \"160MHz\". It then reads back 160MHZ.",
		values: "20MHz, 40MHz, 80MHz, 160MHz (5 and 6 GHz)",
		risk:   "on 5 GHz, 160 MHz only applies if BlackListedChannels leaves a free 160 MHz block (see the README)"},
	{field: "CurrentOperatingChannelBandwidth", scope: "radio", title: "Channel width in use (read only)",
		desc: "Do not trust it alone: we saw it stuck at 80MHz while clients were linked at 160 MHz rates (it reads 160MHz at other times). Check link rates with `clients`."},
	{field: "BlackListedChannels", scope: "radio", bands: []string{"5"}, title: "Channel blacklist (5 GHz)",
		desc:   "Channels the radio must never use, comma separated. Channels depend on the country.",
		values: "comma list, e.g. 104,108,112,116,136,140,144,149,153,161,165",
		risk:   "blacklisting any channel of a 160 MHz block (36-64) silently keeps the radio at 80 MHz"},
	{field: "Channel", scope: "radio", title: "Channel", valuesFrom: "PossibleChannels",
		desc: "Primary channel. Only kept when AutoChannelEnable is False.",
		risk: "5 GHz channels 52-144 are DFS: after a radar detection the radio leaves and the channel is banned for 30 minutes"},
	{field: "AutoChannelEnable", scope: "radio", title: "Automatic channel", values: "True, False",
		desc: "When True the router picks the channel itself and may leave the one you chose."},
	{field: "TransmitPower", scope: "radio", title: "Transmit power (%)", valuesFrom: "TransmitPowerSupported",
		desc: "Normal power setting, in percent."},
	{field: "IncreasedPowerEnable", scope: "radio", title: "Increased power", values: "True, False",
		desc: "Separate hidden switch, not the same thing as TransmitPower. Effect not measured by us.",
		risk: "unknown; keep within your country's rules"},
	{field: "DTIMPeriod", scope: "radio", title: "DTIM period", values: "integer (beacons)",
		desc: "How often buffered broadcast/multicast is sent to sleeping clients. Higher saves phone battery, delays multicast (mDNS, casting)."},
	{field: "RegulatoryDomain", scope: "radio", title: "Regulatory domain (read only)",
		desc: "Country code the channel list comes from (CAI = Canada, indoor, on our unit)."},
	{field: "RegulatoryRegionSubRegion", scope: "radio", title: "Regulatory region (read only)",
		desc: "Region/sub-region (CA/0 on our unit). The channel examples in this project are for CA/0."},
	{field: "RadarDetections", scope: "radio", bands: []string{"5"}, title: "Radar detections (read only)",
		desc: "Radar events seen on 5 GHz DFS channels: explains channel jumps."},
	{field: "RadioMeasurements/NeighborReport", scope: "ap", title: "802.11k neighbour reports", values: "True, False",
		desc: "Lets clients ask which other bands/access points are around, for faster roaming."},
	{field: "DirectMulticast", scope: "ap", title: "Multicast to unicast", values: "True, False",
		desc: "Converts multicast to per-client unicast (faster and more reliable for video, but more air time with many clients)."},
	{field: "Security/ModeEnabled", scope: "ap", title: "Security mode", valuesFrom: "Security/ModesSupported",
		desc: "WPA2 / WPA3 / transition mode of this access point. The router answers WPA2_PERSONAL while listing WPA2-Personal.",
		risk: "every client of this network must reconnect; 6 GHz requires WPA3 (or OWE) by standard"},
	{field: "Security/MFPConfig", scope: "ap", title: "Management frame protection (802.11w)", values: "DISABLED, OPTIONAL, REQUIRED",
		desc: "REQUIRED refuses clients without MFP. Mandatory on 6 GHz.",
		risk: "REQUIRED locks out older devices"},
	{field: "SSID", scope: "ssid", title: "Network name (SSID)",
		desc: "Name per network/band. A separate 6 GHz name stops iPhones from bouncing between WPA2 and WPA3 bands (see README).",
		risk: "devices remembering the old name must be joined again"},
}

type ssidInfo struct {
	alias, band, ap string
	hasName         bool // some internal networks (IPTV) have no SSID field
}

// discover lists the SSIDs with their band and access point (aliases differ between operators).
func discover(c *Client) ([]ssidInfo, error) {
	raw, err := c.GetRaw([]string{"Device/WiFi/SSIDs", "Device/WiFi/AccessPoints"})
	if err != nil {
		return nil, err
	}
	var ss []struct {
		Alias, LowerLayers string
		SSID               *string
	}
	var aps []struct{ Alias, SSIDReference string }
	if json.Unmarshal(raw[0], &ss) != nil || json.Unmarshal(raw[1], &aps) != nil {
		return nil, fmt.Errorf("unexpected Wi-Fi tree")
	}
	var out []ssidInfo
	for _, s := range ss {
		i := ssidInfo{alias: s.Alias, band: "?", hasName: s.SSID != nil}
		for _, r := range radios {
			if strings.Contains(s.LowerLayers, r.alias+"]") || strings.Contains(s.LowerLayers, r.alias+"'") {
				i.band = r.band
			}
		}
		for _, a := range aps {
			if strings.HasSuffix(a.SSIDReference, "["+s.Alias+"]") || strings.Contains(a.SSIDReference, "'"+s.Alias+"'") {
				i.ap = a.Alias
			}
		}
		out = append(out, i)
	}
	return out, nil
}

type target struct{ label, xpath, valuesXPath string }

func targets(o option, ssids []ssidInfo) []target {
	var out []target
	switch o.scope {
	case "radio":
		for _, r := range radios {
			if len(o.bands) > 0 && o.bands[0] != r.band {
				continue
			}
			base := "Device/WiFi/Radios/Radio[Alias='" + r.alias + "']/"
			t := target{label: r.band + " GHz", xpath: base + o.field}
			if o.valuesFrom != "" {
				t.valuesXPath = base + o.valuesFrom
			}
			out = append(out, t)
		}
	case "ap", "ssid":
		if ssids == nil { // offline: show the pattern
			p := "Device/WiFi/AccessPoints/AccessPoint[Alias='<AP>']/" + o.field
			if o.scope == "ssid" {
				p = "Device/WiFi/SSIDs/SSID[Alias='<SSID>']/SSID"
			}
			return []target{{label: "each", xpath: p}}
		}
		for _, s := range ssids {
			t := target{label: s.band + " GHz " + s.alias, xpath: "Device/WiFi/SSIDs/SSID[Alias='" + s.alias + "']/SSID"}
			if o.scope == "ssid" && !s.hasName {
				continue
			}
			if o.scope == "ap" {
				if s.ap == "" {
					continue
				}
				base := "Device/WiFi/AccessPoints/AccessPoint[Alias='" + s.ap + "']/"
				t = target{label: s.band + " GHz " + s.ap, xpath: base + o.field}
				if o.valuesFrom != "" {
					t.valuesXPath = base + o.valuesFrom
				}
			}
			out = append(out, t)
		}
	}
	return out
}

// readMany reads in one request, and falls back to one request per xpath when a path is unknown.
func readMany(c *Client, xps []string) map[string]string {
	vals := map[string]string{}
	if v, err := c.Get(xps); err == nil {
		for i, x := range xps {
			vals[x] = v[i]
		}
		return vals
	}
	for _, x := range xps {
		v, err := c.Get([]string{x})
		if errors.Is(err, ErrRouterDistress) {
			break
		}
		if err != nil {
			vals[x] = "(error: " + err.Error() + ")"
		} else {
			vals[x] = v[0]
		}
	}
	return vals
}

// cmdOptions prints the catalogue, with current values when the router can be reached.
func cmdOptions(cfgPath string) error {
	var c *Client
	cfg, err := loadConfig(cfgPath)
	if err == nil {
		c, err = connect(cfg, 3, 10*time.Second)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "not connected (%v): showing the catalogue without current values\n\n", err)
		printOptions(os.Stdout, nil)
		return nil
	}
	defer c.Logout()
	printOptions(os.Stdout, c)
	return nil
}

func printOptions(w io.Writer, c *Client) {
	var ssids []ssidInfo
	var vals map[string]string
	if c != nil {
		var err error
		if ssids, err = discover(c); err != nil {
			fmt.Fprintf(w, "(could not list the networks: %v)\n\n", err)
		}
		var xps []string
		for _, o := range catalog {
			for _, t := range targets(o, ssids) {
				xps = append(xps, t.xpath)
				if t.valuesXPath != "" {
					xps = append(xps, t.valuesXPath)
				}
			}
		}
		vals = readMany(c, xps)
	}
	for _, o := range catalog {
		fmt.Fprintf(w, "== %s  [%s]\n   %s\n", o.title, o.field, o.desc)
		if o.values != "" {
			fmt.Fprintf(w, "   values: %s\n", o.values)
		}
		if o.risk != "" {
			fmt.Fprintf(w, "   risk: %s\n", o.risk)
		}
		for _, t := range targets(o, ssids) {
			if vals == nil {
				fmt.Fprintf(w, "   %-14s %s\n", t.label, t.xpath)
				continue
			}
			fmt.Fprintf(w, "   %-14s %s = %q\n", t.label, t.xpath, vals[t.xpath])
			if t.valuesXPath != "" && vals[t.valuesXPath] != "" {
				fmt.Fprintf(w, "   %-14s accepted by the router: %s\n", "", vals[t.valuesXPath])
			}
		}
		fmt.Fprintln(w)
	}
	if ssids == nil {
		fmt.Fprintln(w, "<AP> and <SSID> are aliases such as PRIV0 and WL_PRIV_6G; list yours with `dump Device/WiFi/SSIDs`.")
	}
}

// 2-stream clients top out at 1201 Mbit/s on 80 MHz (Wi-Fi 6); above that, 160 MHz is in use.
const max80MHz2ss = 1201

func cmdClients(c *Client) error {
	ssids, err := discover(c)
	if err != nil {
		return err
	}
	band := map[string]string{}
	for _, s := range ssids {
		band[s.ap] = s.band
	}
	raw, err := c.GetRaw([]string{"Device/WiFi/AccessPoints"})
	if err != nil {
		return err
	}
	var aps []struct {
		Alias             string
		AssociatedDevices []struct {
			Active                                   bool
			MACAddress, OperatingStandard            string
			SignalStrength                           int
			LastDataDownlinkRate, LastDataUplinkRate int
		}
	}
	if err := json.Unmarshal(raw[0], &aps); err != nil {
		return fmt.Errorf("unexpected access point tree: %v", err)
	}
	best := map[string]int{}
	for _, a := range aps {
		n := 0
		for _, d := range a.AssociatedDevices {
			if !d.Active {
				continue
			}
			if n == 0 {
				fmt.Printf("%s GHz  %s\n", band[a.Alias], a.Alias)
			}
			n++
			dl := d.LastDataDownlinkRate / 1000
			fmt.Printf("   %s  %4d dBm  down %5d Mbit/s  up %5d Mbit/s  802.11%s\n",
				d.MACAddress, d.SignalStrength, dl, d.LastDataUplinkRate/1000, d.OperatingStandard)
			if dl > best[band[a.Alias]] {
				best[band[a.Alias]] = dl
			}
		}
	}
	var bands []string
	for b := range best {
		bands = append(bands, b)
	}
	sort.Strings(bands)
	for _, b := range bands {
		if b != "5" && b != "6" {
			continue
		}
		verdict := "not proven (no client above the 80 MHz ceiling right now; an idle client reports a low rate)"
		if best[b] > max80MHz2ss {
			verdict = "160 MHz IS in use"
		}
		fmt.Printf("%s GHz: best link %d Mbit/s (80 MHz ceiling for 2 streams: %d) -> %s\n", b, best[b], max80MHz2ss, verdict)
	}
	return nil
}
