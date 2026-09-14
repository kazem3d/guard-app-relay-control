// Package netcfg applies network configuration through NetworkManager over
// D-Bus (github.com/Wifx/gonetworkmanager, falling back to raw
// github.com/godbus/dbus for hostname1). It implements
// httpapi.NetworkConfigurator.
//
// Changing the IP address of the interface carrying the request that asked
// for the change can sever that very request — a typo in a gateway field
// would otherwise strand a headless appliance until someone drives out with
// a monitor and keyboard. Apply guards against this using NetworkManager's
// own Checkpoint/Rollback mechanism: a checkpoint is created with a 60s
// automatic-rollback timeout *before* the change is applied, and only a
// subsequent Confirm() (reachable at whatever address turns out to be
// live) disarms it. If Confirm never arrives, NetworkManager itself
// restores the prior settings — no hand-rolled snapshot/restore needed.
package netcfg

import (
	"fmt"
	"sync"

	nm "github.com/Wifx/gonetworkmanager/v3"
	"github.com/rmto/raspberryrelay/internal/config"
	"github.com/rmto/raspberryrelay/internal/httpapi"
)

// rollbackTimeoutSeconds is how long NetworkManager waits for Confirm()
// before automatically reverting an applied change.
const rollbackTimeoutSeconds = 60

// Manager is the production NetworkConfigurator, backed by NetworkManager.
type Manager struct {
	nm       nm.NetworkManager
	settings nm.Settings

	mu         sync.Mutex
	checkpoint nm.Checkpoint // non-nil while a change is pending confirmation
}

// New connects to NetworkManager over the system D-Bus.
func New() (*Manager, error) {
	n, err := nm.NewNetworkManager()
	if err != nil {
		return nil, fmt.Errorf("netcfg: connecting to NetworkManager: %w", err)
	}
	s, err := nm.NewSettings()
	if err != nil {
		return nil, fmt.Errorf("netcfg: connecting to NetworkManager settings: %w", err)
	}
	return &Manager{nm: n, settings: s}, nil
}

// findWiredDevice returns the first realized Ethernet device. This
// appliance targets a single-NIC Pi, so "first wired device" is an
// intentional simplification rather than a heuristic that needs to be
// right for multi-NIC hosts.
func (m *Manager) findWiredDevice() (nm.Device, error) {
	devices, err := m.nm.GetDevices()
	if err != nil {
		return nil, fmt.Errorf("netcfg: listing devices: %w", err)
	}
	for _, d := range devices {
		dt, err := d.GetPropertyDeviceType()
		if err != nil {
			continue
		}
		if dt == nm.NmDeviceTypeEthernet {
			return d, nil
		}
	}
	return nil, fmt.Errorf("netcfg: no wired ethernet device found")
}

// activeConnectionFor returns the Connection settings object currently
// activated on device.
func activeConnectionFor(device nm.Device) (nm.Connection, error) {
	ac, err := device.GetPropertyActiveConnection()
	if err != nil || ac == nil {
		return nil, fmt.Errorf("netcfg: device has no active connection: %w", err)
	}
	return ac.GetPropertyConnection()
}

// Get reads live network status: address/gateway/DNS from the device's
// current IP4Config (correct whether the interface got there via DHCP or a
// static profile), the configured method (dhcp/static) from the active
// connection's settings, and the hostname from systemd-hostnamed.
func (m *Manager) Get() (httpapi.NetworkStatus, error) {
	device, err := m.findWiredDevice()
	if err != nil {
		return httpapi.NetworkStatus{}, err
	}

	status := httpapi.NetworkStatus{Mode: "dhcp"}

	if conn, err := activeConnectionFor(device); err == nil {
		if settings, err := conn.GetSettings(); err == nil {
			if ipv4, ok := settings["ipv4"]; ok {
				if method, ok := ipv4["method"].(string); ok && method == "manual" {
					status.Mode = "static"
				}
			}
		}
	}

	if ip4cfg, err := device.GetPropertyIP4Config(); err == nil && ip4cfg != nil {
		if addrs, err := ip4cfg.GetPropertyAddressData(); err == nil && len(addrs) > 0 {
			status.Address = addrs[0].Address
			if netmask, err := prefixToNetmask(addrs[0].Prefix); err == nil {
				status.Netmask = netmask
			}
		}
		if gw, err := ip4cfg.GetPropertyGateway(); err == nil {
			status.Gateway = gw
		}
		if ns, err := ip4cfg.GetPropertyNameserverData(); err == nil {
			for _, n := range ns {
				status.DNS = append(status.DNS, n.Address)
			}
		}
	}

	if hostname, err := getStaticHostname(); err == nil {
		status.Hostname = hostname
	}

	return status, nil
}

// Apply applies intent to the wired device, guarded by a NetworkManager
// checkpoint that auto-rolls-back after rollbackTimeoutSeconds unless
// Confirm is called first. Hostname is applied immediately and
// unconditionally via systemd-hostnamed — it does not affect IP
// reachability, so it needs no rollback protection.
func (m *Manager) Apply(intent config.NetworkIntent) error {
	if err := setStaticHostname(intent.Hostname); err != nil {
		return err
	}

	device, err := m.findWiredDevice()
	if err != nil {
		return err
	}
	conn, err := activeConnectionFor(device)
	if err != nil {
		return err
	}
	settings, err := conn.GetSettings()
	if err != nil {
		return fmt.Errorf("netcfg: reading current connection settings: %w", err)
	}

	ipv4, err := buildIPv4Settings(intent)
	if err != nil {
		return err
	}
	settings["ipv4"] = ipv4
	stripLegacyAddressSettings(settings)

	m.mu.Lock()
	defer m.mu.Unlock()

	// A prior change that was never confirmed is superseded by this one;
	// destroying it without confirming lets NetworkManager keep whatever
	// is live right now (which Update+ActivateConnection below is about
	// to change anyway) rather than racing two pending rollbacks.
	if m.checkpoint != nil {
		_ = m.nm.CheckpointDestroy(m.checkpoint)
		m.checkpoint = nil
	}

	checkpoint, err := m.nm.CheckpointCreate([]nm.Device{device}, rollbackTimeoutSeconds, 0)
	if err != nil {
		return fmt.Errorf("netcfg: creating rollback checkpoint: %w", err)
	}

	if err := conn.Update(settings); err != nil {
		_ = m.nm.CheckpointDestroy(checkpoint)
		return fmt.Errorf("netcfg: updating connection settings: %w", err)
	}
	if _, err := m.nm.ActivateConnection(conn, device, nil); err != nil {
		_ = m.nm.CheckpointDestroy(checkpoint)
		return fmt.Errorf("netcfg: activating updated connection: %w", err)
	}

	m.checkpoint = checkpoint
	return nil
}

// Confirm disarms the rollback armed by the most recent Apply, so
// NetworkManager keeps the new settings instead of automatically reverting
// them at the checkpoint's timeout.
func (m *Manager) Confirm() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.checkpoint == nil {
		return
	}
	_ = m.nm.CheckpointDestroy(m.checkpoint)
	m.checkpoint = nil
}

// stripLegacyAddressSettings removes the deprecated packed-array address
// keys (e.g. ipv6.addresses, ipv4.addresses, *.routes) from settings blocks
// that were fetched via conn.GetSettings() but not rebuilt by this package.
// Those keys carry D-Bus struct signatures (e.g. "a(ayuay)" for
// ipv6.addresses) that gonetworkmanager decodes into untyped []interface{}
// values; feeding that back into conn.Update() re-encodes them as "aav"
// instead of the original struct array, which NetworkManager rejects. The
// modern address-data/route-data keys are unaffected and NetworkManager
// regenerates the legacy keys on its own once addresses are set via them.
func stripLegacyAddressSettings(settings map[string]map[string]interface{}) {
	legacyKeys := []string{"addresses", "routes", "address-labels"}
	for _, block := range settings {
		for _, key := range legacyKeys {
			delete(block, key)
		}
	}
}

// buildIPv4Settings translates a config.NetworkIntent into the
// NetworkManager ipv4 settings map. For "static" it uses the modern
// address-data/dns-data string forms rather than the legacy packed-uint32
// addresses/dns keys.
func buildIPv4Settings(intent config.NetworkIntent) (map[string]interface{}, error) {
	if intent.Mode == "dhcp" || intent.Mode == "" {
		return map[string]interface{}{
			"method": "auto",
		}, nil
	}

	prefix, err := netmaskToPrefix(intent.Netmask)
	if err != nil {
		return nil, err
	}

	addressData := []map[string]interface{}{
		{"address": intent.Address, "prefix": prefix},
	}
	dnsData := append([]string(nil), intent.DNS...)

	return map[string]interface{}{
		"method":       "manual",
		"address-data": addressData,
		"gateway":      intent.Gateway,
		"dns-data":     dnsData,
	}, nil
}
