// Package netmgr keeps container IP addresses stable.
//
// Docker's default bridge hands out addresses on a first-come basis, so a
// container's address changes as containers come and go. Fixing an address
// requires a user-defined bridge with a known subnet. netmgr provisions exactly
// one such network ("docman0" by default), picks a free subnet that does not
// clash with anything already on the host, splits it into a static half that it
// allocates from and a dynamic half it leaves to Docker, and moves containers
// onto it on request. The user only ever sees "pin this address".
package netmgr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"

	"docman/internal/dock"
	"docman/internal/store"
)

// Settings keys used to remember the managed network between restarts.
const (
	SettingNetworkName = "managed_network_name"
	SettingSubnet      = "managed_network_subnet"

	// DefaultNetworkName is the network DocMan creates for pinned addresses.
	DefaultNetworkName = "docman0"

	// LabelManaged marks the network as DocMan's, so it is never mistaken for
	// a user network and is recognised again after a restart.
	LabelManaged = "io.docman.managed"
)

// candidateSubnets are tried in order; the first that does not overlap an
// existing docker network wins.
var candidateSubnets = []string{
	"172.20.0.0/16", "172.21.0.0/16", "172.22.0.0/16", "172.23.0.0/16",
	"172.24.0.0/16", "172.25.0.0/16", "172.26.0.0/16", "172.27.0.0/16",
	"172.28.0.0/16", "172.29.0.0/16", "172.30.0.0/16", "172.31.0.0/16",
	"10.210.0.0/16", "10.211.0.0/16", "10.212.0.0/16", "10.213.0.0/16",
	"192.168.216.0/22", "192.168.220.0/22",
}

// Manager owns the managed network and the pin records.
type Manager struct {
	dc *dock.Client
	st *store.Store
}

// New builds a Manager.
func New(dc *dock.Client, st *store.Store) *Manager {
	return &Manager{dc: dc, st: st}
}

// NetworkName is the configured managed network name.
func (m *Manager) NetworkName() string {
	return m.st.Setting(SettingNetworkName, DefaultNetworkName)
}

// Plan describes the managed network's addressing.
type Plan struct {
	Name        string `json:"name"`
	Subnet      string `json:"subnet"`
	Gateway     string `json:"gateway"`
	StaticFirst string `json:"static_first"`
	StaticLast  string `json:"static_last"`
	DynamicFrom string `json:"dynamic_from"`
}

// Ensure returns the managed network, creating it on first use.
func (m *Manager) Ensure(ctx context.Context) (*dock.Network, error) {
	name := m.NetworkName()
	if nw, err := m.dc.InspectNetwork(ctx, name); err == nil {
		if len(nw.IPAM.Config) == 0 || nw.IPAM.Config[0].Subnet == "" {
			return nil, fmt.Errorf("network %q exists but has no subnet; remove it or set a different managed network name", name)
		}
		_ = m.st.SetSetting(SettingSubnet, nw.IPAM.Config[0].Subnet)
		return nw, nil
	} else if !dock.NotFound(err) {
		return nil, err
	}

	subnet, err := m.pickSubnet(ctx)
	if err != nil {
		return nil, err
	}
	plan, err := planFor(name, subnet)
	if err != nil {
		return nil, err
	}
	req := &dock.CreateNetworkRequest{
		Name:       name,
		Driver:     "bridge",
		Attachable: true,
		Labels: map[string]string{
			LabelManaged:        "true",
			"io.docman.purpose": "fixed container addresses",
		},
		Options: map[string]string{
			"com.docker.network.bridge.name":                 name,
			"com.docker.network.bridge.enable_icc":           "true",
			"com.docker.network.bridge.enable_ip_masquerade": "true",
		},
	}
	req.IPAM = &dock.NetworkIPAM{
		Driver: "default",
		Config: []dock.IPAMConfig{{
			Subnet:  plan.Subnet,
			Gateway: plan.Gateway,
			IPRange: plan.DynamicFrom,
		}},
	}
	if _, err := m.dc.CreateNetwork(ctx, req); err != nil {
		return nil, fmt.Errorf("create managed network: %w", err)
	}
	_ = m.st.SetSetting(SettingNetworkName, name)
	_ = m.st.SetSetting(SettingSubnet, plan.Subnet)
	return m.dc.InspectNetwork(ctx, name)
}

// Plan reports the addressing of the managed network without creating it.
func (m *Manager) Plan(ctx context.Context) (*Plan, error) {
	name := m.NetworkName()
	nw, err := m.dc.InspectNetwork(ctx, name)
	if err != nil {
		if dock.NotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(nw.IPAM.Config) == 0 {
		return nil, fmt.Errorf("network %q has no subnet", name)
	}
	return planFor(name, nw.IPAM.Config[0].Subnet)
}

func (m *Manager) pickSubnet(ctx context.Context) (string, error) {
	networks, err := m.dc.ListNetworks(ctx)
	if err != nil {
		return "", err
	}
	var taken []*net.IPNet
	for _, nw := range networks {
		for _, cfg := range nw.IPAM.Config {
			if cfg.Subnet == "" {
				continue
			}
			if _, n, err := net.ParseCIDR(cfg.Subnet); err == nil {
				taken = append(taken, n)
			}
		}
	}
	// Also avoid the addresses of the host's own interfaces.
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				taken = append(taken, ipnet)
			}
		}
	}
	for _, candidate := range candidateSubnets {
		_, cnet, err := net.ParseCIDR(candidate)
		if err != nil {
			continue
		}
		clash := false
		for _, t := range taken {
			if overlaps(cnet, t) {
				clash = true
				break
			}
		}
		if !clash {
			return candidate, nil
		}
	}
	return "", errors.New("no free private subnet available for the managed network; free one up or set a subnet manually")
}

func overlaps(a, b *net.IPNet) bool {
	return a.Contains(b.IP) || b.Contains(a.IP)
}

// planFor splits a subnet into a static lower half DocMan allocates from and a
// dynamic upper half Docker keeps for itself.
func planFor(name, subnet string) (*Plan, error) {
	ip, cidr, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, fmt.Errorf("invalid subnet %q: %w", subnet, err)
	}
	if ip.To4() == nil {
		return nil, fmt.Errorf("subnet %q is not IPv4", subnet)
	}
	ones, bits := cidr.Mask.Size()
	if bits != 32 || ones > 28 {
		return nil, fmt.Errorf("subnet %q is too small to split", subnet)
	}
	base := cidr.IP.Mask(cidr.Mask).To4()
	gateway := addOffset(base, 1)
	staticFirst := addOffset(base, 10)

	// The dynamic half starts halfway through the subnet.
	dynBits := ones + 1
	dynBase := addOffset(base, 1<<uint(32-dynBits))
	staticLast := addOffset(dynBase, -1)

	return &Plan{
		Name:        name,
		Subnet:      cidr.String(),
		Gateway:     gateway.String(),
		StaticFirst: staticFirst.String(),
		StaticLast:  staticLast.String(),
		DynamicFrom: fmt.Sprintf("%s/%d", dynBase.String(), dynBits),
	}, nil
}

func addOffset(ip net.IP, offset int) net.IP {
	v := ip.To4()
	if v == nil {
		return ip
	}
	n := int64(v[0])<<24 | int64(v[1])<<16 | int64(v[2])<<8 | int64(v[3])
	n += int64(offset)
	out := make(net.IP, 4)
	out[0] = byte(n >> 24)
	out[1] = byte(n >> 16)
	out[2] = byte(n >> 8)
	out[3] = byte(n)
	return out
}

func ipToInt(ip net.IP) int64 {
	v := ip.To4()
	if v == nil {
		return -1
	}
	return int64(v[0])<<24 | int64(v[1])<<16 | int64(v[2])<<8 | int64(v[3])
}

// PinResult reports the outcome of pinning a container.
type PinResult struct {
	Container string `json:"container"`
	Network   string `json:"network"`
	IP        string `json:"ip"`
	Applied   bool   `json:"applied"`
	Note      string `json:"note,omitempty"`
}

// Pin fixes a container's address on the managed network. wantIP may be empty
// to let DocMan allocate the next free static address.
func (m *Manager) Pin(ctx context.Context, containerRef, wantIP string) (*PinResult, error) {
	inspect, err := m.dc.InspectContainer(ctx, containerRef)
	if err != nil {
		return nil, err
	}
	name := containerName(inspect)
	if name == "" {
		return nil, errors.New("could not determine container name")
	}
	mode := networkMode(inspect)
	switch {
	case mode == "host":
		return nil, errors.New("this container shares the host network, so it has no address of its own to fix")
	case mode == "none":
		return nil, errors.New("this container has networking disabled")
	case strings.HasPrefix(mode, "container:"):
		return nil, errors.New("this container shares another container's network namespace")
	}

	nw, err := m.Ensure(ctx)
	if err != nil {
		return nil, err
	}
	plan, err := planFor(nw.Name, nw.IPAM.Config[0].Subnet)
	if err != nil {
		return nil, err
	}

	ip := strings.TrimSpace(wantIP)
	if ip == "" {
		ip, err = m.nextFreeIP(ctx, plan, name)
		if err != nil {
			return nil, err
		}
	} else if err := m.validateIP(ctx, plan, ip, name); err != nil {
		return nil, err
	}

	attachments := dock.NetworkAttachments(inspect)
	// Re-attaching is the only way to change a fixed address.
	if _, already := attachments[nw.Name]; already {
		if err := m.dc.DisconnectNetwork(ctx, nw.Name, name, true); err != nil && !dock.NotFound(err) {
			return nil, fmt.Errorf("detach from %s: %w", nw.Name, err)
		}
	}
	endpoint := &dock.EndpointSettings{
		IPAMConfig: &dock.EndpointIPAM{IPv4Address: ip},
		Aliases:    []string{name},
	}
	if err := m.dc.ConnectNetwork(ctx, nw.Name, name, endpoint); err != nil {
		return nil, fmt.Errorf("attach %s at %s: %w", name, ip, err)
	}

	note := ""
	// Leaving the default bridge is what makes the address the container's only
	// one; user-defined networks the operator chose are left alone.
	if _, onBridge := attachments["bridge"]; onBridge {
		if err := m.dc.DisconnectNetwork(ctx, "bridge", name, true); err != nil {
			note = "kept the default bridge attachment: " + err.Error()
		}
	}
	if err := m.st.SavePin(name, nw.Name, ip); err != nil {
		return nil, err
	}
	return &PinResult{Container: name, Network: nw.Name, IP: ip, Applied: true, Note: note}, nil
}

// Unpin releases a fixed address and returns the container to the default bridge.
func (m *Manager) Unpin(ctx context.Context, containerRef string) error {
	name := containerRef
	if inspect, err := m.dc.InspectContainer(ctx, containerRef); err == nil {
		if n := containerName(inspect); n != "" {
			name = n
		}
	}
	pin, err := m.st.Pin(name)
	network := m.NetworkName()
	if err == nil {
		network = pin.Network
	}
	if err := m.dc.DisconnectNetwork(ctx, network, name, true); err != nil && !dock.NotFound(err) {
		// A container that is already detached is not an error worth surfacing.
		if !strings.Contains(strings.ToLower(err.Error()), "not connected") {
			return err
		}
	}
	// Without any network a container would be unreachable, so restore the default.
	if inspect, err := m.dc.InspectContainer(ctx, name); err == nil {
		if len(dock.NetworkAttachments(inspect)) == 0 {
			_ = m.dc.ConnectNetwork(ctx, "bridge", name, nil)
		}
	}
	return m.st.DeletePin(name)
}

func (m *Manager) validateIP(ctx context.Context, plan *Plan, ip, self string) error {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		return fmt.Errorf("%q is not an IPv4 address", ip)
	}
	_, cidr, err := net.ParseCIDR(plan.Subnet)
	if err != nil {
		return err
	}
	if !cidr.Contains(parsed) {
		return fmt.Errorf("%s is outside the managed subnet %s", ip, plan.Subnet)
	}
	n := ipToInt(parsed)
	if n < ipToInt(net.ParseIP(plan.StaticFirst)) || n > ipToInt(net.ParseIP(plan.StaticLast)) {
		return fmt.Errorf("%s is outside the fixed-address range %s-%s", ip, plan.StaticFirst, plan.StaticLast)
	}
	used, err := m.usedIPs(ctx, self)
	if err != nil {
		return err
	}
	if owner, taken := used[ip]; taken {
		return fmt.Errorf("%s is already used by %s", ip, owner)
	}
	return nil
}

// usedIPs maps every address in use on the managed network to its holder,
// excluding the container named self.
func (m *Manager) usedIPs(ctx context.Context, self string) (map[string]string, error) {
	used := map[string]string{}
	nw, err := m.dc.InspectNetwork(ctx, m.NetworkName())
	if err != nil && !dock.NotFound(err) {
		return nil, err
	}
	if nw != nil {
		for _, c := range nw.Containers {
			if c.Name == self {
				continue
			}
			if host, _, err := net.ParseCIDR(c.IPv4Address); err == nil {
				used[host.String()] = c.Name
			} else if ip := net.ParseIP(c.IPv4Address); ip != nil {
				used[ip.String()] = c.Name
			}
		}
	}
	pins, err := m.st.ListPins()
	if err != nil {
		return nil, err
	}
	for _, p := range pins {
		if p.Container == self {
			continue
		}
		if _, ok := used[p.IP]; !ok {
			used[p.IP] = p.Container
		}
	}
	return used, nil
}

func (m *Manager) nextFreeIP(ctx context.Context, plan *Plan, self string) (string, error) {
	used, err := m.usedIPs(ctx, self)
	if err != nil {
		return "", err
	}
	first := ipToInt(net.ParseIP(plan.StaticFirst))
	last := ipToInt(net.ParseIP(plan.StaticLast))
	if first < 0 || last < first {
		return "", errors.New("managed subnet has no usable fixed-address range")
	}
	for n := first; n <= last; n++ {
		candidate := addOffset(net.ParseIP(plan.StaticFirst), int(n-first)).String()
		if _, taken := used[candidate]; !taken {
			return candidate, nil
		}
	}
	return "", errors.New("no free fixed address left in the managed subnet")
}

// PinState is a pin plus what the host actually shows right now.
type PinState struct {
	Container string `json:"container"`
	Network   string `json:"network"`
	IP        string `json:"ip"`
	LiveIP    string `json:"live_ip"`
	Exists    bool   `json:"exists"`
	InSync    bool   `json:"in_sync"`
	CreatedAt int64  `json:"created_at"`
}

// Status reports the managed network plan and every pin's live state.
func (m *Manager) Status(ctx context.Context) (*Plan, []*PinState, error) {
	plan, err := m.Plan(ctx)
	if err != nil {
		return nil, nil, err
	}
	pins, err := m.st.ListPins()
	if err != nil {
		return nil, nil, err
	}
	live := map[string]string{}
	if plan != nil {
		if nw, err := m.dc.InspectNetwork(ctx, plan.Name); err == nil {
			for _, c := range nw.Containers {
				if host, _, err := net.ParseCIDR(c.IPv4Address); err == nil {
					live[c.Name] = host.String()
				}
			}
		}
	}
	out := make([]*PinState, 0, len(pins))
	for _, p := range pins {
		liveIP, exists := live[p.Container]
		out = append(out, &PinState{
			Container: p.Container,
			Network:   p.Network,
			IP:        p.IP,
			LiveIP:    liveIP,
			Exists:    exists,
			InSync:    exists && liveIP == p.IP,
			CreatedAt: p.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return ipToInt(net.ParseIP(out[i].IP)) < ipToInt(net.ParseIP(out[j].IP)) })
	return plan, out, nil
}

// Recover rebuilds fixed-address records from what Docker itself holds, for
// when DocMan's database has lost them — a fresh data volume, a restore from
// an old backup. A container attached to DocMan's managed network with an
// address it asked for explicitly (a static IPAM address, rather than one
// Docker handed out) can only have been given it by DocMan, so it is recorded
// again. Existing records are never changed.
func (m *Manager) Recover(ctx context.Context) []string {
	name := m.NetworkName()
	nw, err := m.dc.InspectNetwork(ctx, name)
	if err != nil {
		return nil
	}
	// Only adopt DocMan's own network, recognised by its label.
	if nw.Labels[LabelManaged] != "true" {
		return nil
	}
	if len(nw.IPAM.Config) > 0 && nw.IPAM.Config[0].Subnet != "" {
		_ = m.st.SetSetting(SettingNetworkName, nw.Name)
		_ = m.st.SetSetting(SettingSubnet, nw.IPAM.Config[0].Subnet)
	}
	var notes []string
	for id := range nw.Containers {
		inspect, err := m.dc.InspectContainer(ctx, id)
		if err != nil {
			continue
		}
		cname := containerName(inspect)
		if cname == "" {
			continue
		}
		if _, err := m.st.Pin(cname); err == nil {
			continue // already recorded
		}
		ep, ok := dock.NetworkAttachments(inspect)[nw.Name]
		if !ok || ep.IPAMConfig == nil || ep.IPAMConfig.IPv4Address == "" {
			continue // an address Docker chose, not a fixed one
		}
		if err := m.st.SavePin(cname, nw.Name, ep.IPAMConfig.IPv4Address); err != nil {
			continue
		}
		notes = append(notes, "recovered the fixed address "+ep.IPAMConfig.IPv4Address+" of "+cname)
	}
	return notes
}

// Reconcile re-applies pins that drifted, which happens when a container is
// recreated or the daemon reassigns addresses after a restart.
func (m *Manager) Reconcile(ctx context.Context) []string {
	// Records lost from the database come back from Docker first.
	notes := m.Recover(ctx)
	pins, err := m.st.ListPins()
	if err != nil || len(pins) == 0 {
		return notes
	}
	for _, p := range pins {
		inspect, err := m.dc.InspectContainer(ctx, p.Container)
		if err != nil {
			if dock.NotFound(err) {
				notes = append(notes, "pinned container "+p.Container+" no longer exists")
			}
			continue
		}
		attachments := dock.NetworkAttachments(inspect)
		ep, attached := attachments[p.Network]
		if attached && ep.IPAMConfig != nil && ep.IPAMConfig.IPv4Address == p.IP {
			continue
		}
		if _, err := m.Pin(ctx, p.Container, p.IP); err != nil {
			notes = append(notes, "could not restore "+p.IP+" for "+p.Container+": "+err.Error())
			continue
		}
		notes = append(notes, "restored "+p.IP+" for "+p.Container)
	}
	return notes
}

// ApplyPinToCreateBody injects a container's pinned address into a create body,
// so a recreate comes back up on the same address.
func (m *Manager) ApplyPinToCreateBody(name string, body map[string]any) {
	pin, err := m.st.Pin(name)
	if err != nil {
		return
	}
	endpoints := map[string]any{
		pin.Network: map[string]any{
			"IPAMConfig": map[string]any{"IPv4Address": pin.IP},
			"Aliases":    []string{name},
		},
	}
	body["NetworkingConfig"] = map[string]any{"EndpointsConfig": endpoints}
	if host, ok := body["HostConfig"].(map[string]any); ok {
		host["NetworkMode"] = pin.Network
	}
}

func containerName(inspect map[string]any) string {
	if v, ok := inspect["Name"].(string); ok {
		return strings.TrimPrefix(v, "/")
	}
	return ""
}

func networkMode(inspect map[string]any) string {
	host, ok := inspect["HostConfig"].(map[string]any)
	if !ok {
		return ""
	}
	mode, _ := host["NetworkMode"].(string)
	return mode
}
