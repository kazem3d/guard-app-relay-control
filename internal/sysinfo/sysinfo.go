// Package sysinfo reads device information for the /api/device endpoint:
// hostname, primary IP/MAC, uptime, CPU temperature, and board model. Every
// value comes from a plain file or syscall read with no external
// dependencies, and each field degrades to a zero value independently
// rather than failing the whole response — several of these paths (the
// thermal zone, the device-tree model) don't exist on a development
// machine, and the endpoint should still return something useful there.
package sysinfo

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Info is the payload for GET /api/device.
type Info struct {
	Hostname       string   `json:"hostname"`
	IP             string   `json:"ip"`
	MAC            string   `json:"mac"`
	Firmware       string   `json:"firmware"`
	Model          string   `json:"model,omitempty"`
	Uptime         string   `json:"uptime"`
	UptimeSeconds  int64    `json:"uptime_seconds"`
	CPUTempCelsius *float64 `json:"cpu_temp_celsius,omitempty"`
}

// Collect gathers current device information. version is the build-stamped
// firmware version string.
func Collect(version string) Info {
	info := Info{Firmware: version}

	if h, err := os.Hostname(); err == nil {
		info.Hostname = h
	}

	if ip, mac := primaryInterface(); ip != "" {
		info.IP = ip
		info.MAC = mac
	}

	if secs, ok := uptimeSeconds(); ok {
		info.UptimeSeconds = secs
		info.Uptime = formatUptime(secs)
	}

	if temp, ok := cpuTempCelsius(); ok {
		info.CPUTempCelsius = &temp
	}

	if model, ok := deviceModel(); ok {
		info.Model = model
	}

	return info
}

// primaryInterface returns the IP and MAC of the first non-loopback
// interface that has an IPv4 address and is up. On a machine with several
// interfaces this is a heuristic, not a guarantee, but is right for the
// single-NIC appliance this targets.
func primaryInterface() (ip string, mac string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ipNet *net.IPNet
			switch v := a.(type) {
			case *net.IPNet:
				ipNet = v
			}
			if ipNet == nil || ipNet.IP.To4() == nil {
				continue
			}
			return ipNet.IP.String(), iface.HardwareAddr.String()
		}
	}
	return "", ""
}

func uptimeSeconds() (int64, bool) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, false
	}
	return int64(f), true
}

func formatUptime(seconds int64) string {
	d := time.Duration(seconds) * time.Second
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d days %d hours", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d hours %d minutes", hours, minutes)
	default:
		return fmt.Sprintf("%d minutes", minutes)
	}
}

// cpuTempCelsius reads the SoC thermal zone. Absent on non-Pi hardware
// (this dev machine included), which is why it returns ok=false rather
// than an error.
func cpuTempCelsius() (float64, bool) {
	b, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0, false
	}
	milli, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, false
	}
	return float64(milli) / 1000.0, true
}

func deviceModel() (string, bool) {
	b, err := os.ReadFile("/proc/device-tree/model")
	if err != nil {
		return "", false
	}
	return strings.TrimRight(string(b), "\x00\n"), true
}
