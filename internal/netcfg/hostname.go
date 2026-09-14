package netcfg

import (
	"fmt"

	"github.com/godbus/dbus/v5"
)

// setStaticHostname sets the persistent system hostname via
// org.freedesktop.hostname1 (systemd-hostnamed), not through NetworkManager
// — hostname is a systemd concern, and NM only reads it back.
func setStaticHostname(hostname string) error {
	if hostname == "" {
		return nil
	}
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return fmt.Errorf("netcfg: connecting to system bus: %w", err)
	}
	defer conn.Close()

	obj := conn.Object("org.freedesktop.hostname1", "/org/freedesktop/hostname1")
	// interactive=false: fail rather than prompt if polkit denies it; the
	// daemon runs unattended and has its own polkit rule (see
	// deploy/90-relayd.rules) granting org.freedesktop.hostname1.set-static-hostname.
	call := obj.Call("org.freedesktop.hostname1.SetStaticHostname", 0, hostname, false)
	if call.Err != nil {
		return fmt.Errorf("netcfg: SetStaticHostname: %w", call.Err)
	}
	return nil
}

// getStaticHostname reads the persistent hostname the same way.
func getStaticHostname() (string, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return "", fmt.Errorf("netcfg: connecting to system bus: %w", err)
	}
	defer conn.Close()

	obj := conn.Object("org.freedesktop.hostname1", "/org/freedesktop/hostname1")
	v, err := obj.GetProperty("org.freedesktop.hostname1.StaticHostname")
	if err != nil {
		return "", fmt.Errorf("netcfg: reading StaticHostname: %w", err)
	}
	s, _ := v.Value().(string)
	return s, nil
}
