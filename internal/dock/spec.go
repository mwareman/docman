package dock

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// ContainerSpec is the editable view of a container that DocMan's UI and API
// work with. It deliberately covers the settings people actually change; every
// other field of the original container survives a recreate because
// CreateBodyFrom starts from the live inspect document rather than a fresh one.
type ContainerSpec struct {
	Name       string            `json:"name"`
	Image      string            `json:"image"`
	Entrypoint []string          `json:"entrypoint"`
	Cmd        []string          `json:"cmd"`
	Env        []string          `json:"env"`
	Labels     map[string]string `json:"labels"`
	WorkingDir string            `json:"working_dir"`
	User       string            `json:"user"`
	Hostname   string            `json:"hostname"`
	Tty        bool              `json:"tty"`
	OpenStdin  bool              `json:"open_stdin"`
	StopSignal string            `json:"stop_signal"`

	Ports  []PortMapping `json:"ports"`
	Mounts []MountSpec   `json:"mounts"`

	RestartPolicy  string `json:"restart_policy"`
	RestartRetries int    `json:"restart_retries"`
	AutoRemove     bool   `json:"auto_remove"`

	NetworkMode string   `json:"network_mode"`
	DNS         []string `json:"dns"`
	ExtraHosts  []string `json:"extra_hosts"`

	Privileged     bool     `json:"privileged"`
	ReadOnlyRootfs bool     `json:"read_only_rootfs"`
	CapAdd         []string `json:"cap_add"`
	CapDrop        []string `json:"cap_drop"`
	Devices        []string `json:"devices"`
	GroupAdd       []string `json:"group_add"`
	SecurityOpt    []string `json:"security_opt"`

	Memory     int64             `json:"memory"`
	MemorySwap int64             `json:"memory_swap"`
	NanoCPUs   int64             `json:"nano_cpus"`
	CPUShares  int64             `json:"cpu_shares"`
	PidsLimit  int64             `json:"pids_limit"`
	ShmSize    int64             `json:"shm_size"`
	Sysctls    map[string]string `json:"sysctls"`

	LogDriver  string            `json:"log_driver"`
	LogOptions map[string]string `json:"log_options"`
	Runtime    string            `json:"runtime"`
}

// PortMapping is one published port.
type PortMapping struct {
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
	HostIP        string `json:"host_ip"`
	HostPort      string `json:"host_port"`
}

// MountSpec is one mount attached to a container.
type MountSpec struct {
	Type     string `json:"type"` // volume | bind | tmpfs
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

// ---------- generic map helpers ----------

func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	buf, err := json.Marshal(m)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(buf, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func mapAt(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return nil
}

func sliceAt(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	if v, ok := m[key].([]any); ok {
		return v
	}
	return nil
}

func strAt(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func boolAt(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func i64At(m map[string]any, key string) int64 {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}

func toStrSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func toStrMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		switch t := val.(type) {
		case string:
			out[k] = t
		case float64:
			out[k] = strconv.FormatInt(int64(t), 10)
		case bool:
			out[k] = strconv.FormatBool(t)
		}
	}
	return out
}

// ---------- reading a spec out of an inspect document ----------

// SpecFrom derives the editable spec from a container inspect document.
func SpecFrom(inspect map[string]any) *ContainerSpec {
	cfg := mapAt(inspect, "Config")
	host := mapAt(inspect, "HostConfig")

	s := &ContainerSpec{
		Name:           strings.TrimPrefix(strAt(inspect, "Name"), "/"),
		Image:          strAt(cfg, "Image"),
		Entrypoint:     toStrSlice(cfg["Entrypoint"]),
		Cmd:            toStrSlice(cfg["Cmd"]),
		Env:            toStrSlice(cfg["Env"]),
		Labels:         toStrMap(cfg["Labels"]),
		WorkingDir:     strAt(cfg, "WorkingDir"),
		User:           strAt(cfg, "User"),
		Hostname:       strAt(cfg, "Hostname"),
		Tty:            boolAt(cfg, "Tty"),
		OpenStdin:      boolAt(cfg, "OpenStdin"),
		StopSignal:     strAt(cfg, "StopSignal"),
		AutoRemove:     boolAt(host, "AutoRemove"),
		NetworkMode:    strAt(host, "NetworkMode"),
		DNS:            toStrSlice(host["Dns"]),
		ExtraHosts:     toStrSlice(host["ExtraHosts"]),
		Privileged:     boolAt(host, "Privileged"),
		ReadOnlyRootfs: boolAt(host, "ReadonlyRootfs"),
		CapAdd:         toStrSlice(host["CapAdd"]),
		CapDrop:        toStrSlice(host["CapDrop"]),
		GroupAdd:       toStrSlice(host["GroupAdd"]),
		SecurityOpt:    toStrSlice(host["SecurityOpt"]),
		Memory:         i64At(host, "Memory"),
		MemorySwap:     i64At(host, "MemorySwap"),
		NanoCPUs:       i64At(host, "NanoCpus"),
		CPUShares:      i64At(host, "CpuShares"),
		PidsLimit:      i64At(host, "PidsLimit"),
		ShmSize:        i64At(host, "ShmSize"),
		Sysctls:        toStrMap(host["Sysctls"]),
		Runtime:        strAt(host, "Runtime"),
	}
	if s.Labels == nil {
		s.Labels = map[string]string{}
	}

	if rp := mapAt(host, "RestartPolicy"); rp != nil {
		s.RestartPolicy = strAt(rp, "Name")
		s.RestartRetries = int(i64At(rp, "MaximumRetryCount"))
	}
	if s.RestartPolicy == "" {
		s.RestartPolicy = "no"
	}

	if lc := mapAt(host, "LogConfig"); lc != nil {
		s.LogDriver = strAt(lc, "Type")
		s.LogOptions = toStrMap(lc["Config"])
	}

	for _, d := range sliceAt(host, "Devices") {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		entry := strAt(dm, "PathOnHost") + ":" + strAt(dm, "PathInContainer")
		if perm := strAt(dm, "CgroupPermissions"); perm != "" && perm != "rwm" {
			entry += ":" + perm
		}
		s.Devices = append(s.Devices, entry)
	}

	s.Ports = portsFrom(host)
	s.Mounts = mountsFrom(cfg, host)
	return s
}

func portsFrom(host map[string]any) []PortMapping {
	out := []PortMapping{}
	bindings := mapAt(host, "PortBindings")
	for portProto, v := range bindings {
		port, proto := splitPortProto(portProto)
		if port == 0 {
			continue
		}
		list, _ := v.([]any)
		if len(list) == 0 {
			out = append(out, PortMapping{ContainerPort: port, Protocol: proto})
			continue
		}
		for _, b := range list {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, PortMapping{
				ContainerPort: port,
				Protocol:      proto,
				HostIP:        strAt(bm, "HostIp"),
				HostPort:      strAt(bm, "HostPort"),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ContainerPort != out[j].ContainerPort {
			return out[i].ContainerPort < out[j].ContainerPort
		}
		return out[i].Protocol < out[j].Protocol
	})
	return out
}

func splitPortProto(s string) (int, string) {
	proto := "tcp"
	if i := strings.IndexByte(s, '/'); i >= 0 {
		proto = s[i+1:]
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, proto
	}
	return n, proto
}

func mountsFrom(cfg, host map[string]any) []MountSpec {
	out := []MountSpec{}
	seen := map[string]bool{}

	for _, b := range toStrSlice(host["Binds"]) {
		m := parseBind(b)
		if m.Target == "" || seen[m.Target] {
			continue
		}
		seen[m.Target] = true
		out = append(out, m)
	}
	for _, raw := range sliceAt(host, "Mounts") {
		mm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		target := strAt(mm, "Target")
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		typ := strAt(mm, "Type")
		if typ == "" {
			typ = "volume"
		}
		out = append(out, MountSpec{
			Type:     typ,
			Source:   strAt(mm, "Source"),
			Target:   target,
			ReadOnly: boolAt(mm, "ReadOnly"),
		})
	}
	for target := range mapAt(host, "Tmpfs") {
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, MountSpec{Type: "tmpfs", Target: target})
	}
	// Anonymous volumes declared by the image or with -v /path.
	for target := range mapAt(cfg, "Volumes") {
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, MountSpec{Type: "volume", Target: target})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out
}

func parseBind(b string) MountSpec {
	// host-path-or-volume-name:container-path[:options]
	parts := strings.Split(b, ":")
	m := MountSpec{Type: "volume"}
	switch len(parts) {
	case 1:
		m.Target = parts[0]
	case 2:
		m.Source, m.Target = parts[0], parts[1]
	default:
		m.Source, m.Target = parts[0], parts[1]
		for _, opt := range strings.Split(parts[2], ",") {
			if opt == "ro" {
				m.ReadOnly = true
			}
		}
	}
	if strings.HasPrefix(m.Source, "/") || strings.HasPrefix(m.Source, "./") {
		m.Type = "bind"
	}
	return m
}

// ---------- writing a spec into a create body ----------

// CreateBodyFrom builds a POST /containers/create body from a live inspect
// document with spec applied on top. Passing a nil spec reproduces the
// container exactly as it is, which is what a plain "recreate" needs.
func CreateBodyFrom(inspect map[string]any, spec *ContainerSpec) map[string]any {
	cfg := deepCopyMap(mapAt(inspect, "Config"))
	host := deepCopyMap(mapAt(inspect, "HostConfig"))

	// ConsoleSize is reported by inspect but is not a create-time setting.
	delete(host, "ConsoleSize")

	if spec != nil {
		applyConfig(cfg, spec)
		applyHostConfig(host, spec)
	}

	// A hostname equal to the old short id is Docker's default, not a choice.
	// Copying it would give the replacement its predecessor's id as a hostname.
	if h := strAt(cfg, "Hostname"); h != "" && h == shortID(strAt(inspect, "Id")) {
		delete(cfg, "Hostname")
	}

	body := cfg
	body["HostConfig"] = host
	keepAnonymousVolumes(inspect, host)

	if nc := endpointsFrom(inspect, host); nc != nil {
		body["NetworkingConfig"] = nc
	}
	return body
}

// keepAnonymousVolumes makes the replacement reuse the container's anonymous
// volumes — those the image declares, or made with a bare `-v /path` — instead
// of getting new, empty ones. Without this a recreate leaves the data behind in
// a volume nothing uses, and the container starts over empty. Any path the new
// configuration mounts some other way keeps that choice.
func keepAnonymousVolumes(inspect, host map[string]any) {
	covered := map[string]bool{}
	for _, b := range toStrSlice(host["Binds"]) {
		if m := parseBind(b); m.Target != "" {
			covered[m.Target] = true
		}
	}
	for _, raw := range sliceAt(host, "Mounts") {
		if mm, ok := raw.(map[string]any); ok {
			covered[strAt(mm, "Target")] = true
		}
	}
	for target := range mapAt(host, "Tmpfs") {
		covered[target] = true
	}
	binds := toStrSlice(host["Binds"])
	added := false
	for _, raw := range sliceAt(inspect, "Mounts") {
		mm, ok := raw.(map[string]any)
		if !ok || strAt(mm, "Type") != "volume" {
			continue
		}
		name, dest := strAt(mm, "Name"), strAt(mm, "Destination")
		if name == "" || dest == "" || covered[dest] || !isAnonymousVolumeName(name) {
			continue
		}
		entry := name + ":" + dest
		if rw, ok := mm["RW"].(bool); ok && !rw {
			entry += ":ro"
		}
		binds = append(binds, entry)
		covered[dest] = true
		added = true
	}
	if added {
		host["Binds"] = binds
	}
}

// isAnonymousVolumeName reports Docker's name for an anonymous volume: 64
// lowercase hex characters.
func isAnonymousVolumeName(name string) bool {
	if len(name) != 64 {
		return false
	}
	for _, c := range name {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// IsAnonymousVolumeName is isAnonymousVolumeName for other packages.
func IsAnonymousVolumeName(name string) bool { return isAnonymousVolumeName(name) }

func applyConfig(cfg map[string]any, s *ContainerSpec) {
	if s.Image != "" {
		cfg["Image"] = s.Image
	}
	cfg["Env"] = s.Env
	cfg["Labels"] = s.Labels
	cfg["Tty"] = s.Tty
	cfg["OpenStdin"] = s.OpenStdin
	cfg["StdinOnce"] = false
	cfg["AttachStdin"] = s.OpenStdin
	cfg["WorkingDir"] = s.WorkingDir
	cfg["User"] = s.User
	cfg["Hostname"] = s.Hostname
	if s.StopSignal != "" {
		cfg["StopSignal"] = s.StopSignal
	}

	// An empty slice means "use the image default", which the Engine expresses
	// as a null value; an explicit list overrides it.
	if len(s.Cmd) > 0 {
		cfg["Cmd"] = s.Cmd
	} else {
		cfg["Cmd"] = nil
	}
	if len(s.Entrypoint) > 0 {
		cfg["Entrypoint"] = s.Entrypoint
	} else {
		cfg["Entrypoint"] = nil
	}
	// ArgsEscaped is a Windows-image artefact that confuses create when the
	// command is being replaced.
	delete(cfg, "ArgsEscaped")

	exposed := map[string]any{}
	for _, p := range s.Ports {
		if p.ContainerPort <= 0 {
			continue
		}
		exposed[portKey(p)] = map[string]any{}
	}
	cfg["ExposedPorts"] = exposed

	anon := map[string]any{}
	for _, m := range s.Mounts {
		if m.Type == "volume" && m.Source == "" && m.Target != "" {
			anon[m.Target] = map[string]any{}
		}
	}
	cfg["Volumes"] = anon
}

func applyHostConfig(host map[string]any, s *ContainerSpec) {
	bindings := map[string]any{}
	for _, p := range s.Ports {
		if p.ContainerPort <= 0 {
			continue
		}
		key := portKey(p)
		entry := map[string]any{"HostIp": p.HostIP, "HostPort": p.HostPort}
		list, _ := bindings[key].([]any)
		bindings[key] = append(list, entry)
	}
	host["PortBindings"] = bindings

	binds := []string{}
	tmpfs := map[string]any{}
	for _, m := range s.Mounts {
		if m.Target == "" {
			continue
		}
		switch m.Type {
		case "tmpfs":
			tmpfs[m.Target] = ""
		case "bind", "volume":
			if m.Source == "" {
				continue // handled as an anonymous volume in Config.Volumes
			}
			entry := m.Source + ":" + m.Target
			if m.ReadOnly {
				entry += ":ro"
			}
			binds = append(binds, entry)
		}
	}
	host["Binds"] = binds
	host["Tmpfs"] = tmpfs
	// Everything is expressed through Binds/Tmpfs, so the richer Mounts array
	// must be cleared or the Engine will reject duplicate targets.
	host["Mounts"] = nil

	policy := s.RestartPolicy
	if policy == "" {
		policy = "no"
	}
	rp := map[string]any{"Name": policy, "MaximumRetryCount": 0}
	if policy == "on-failure" && s.RestartRetries > 0 {
		rp["MaximumRetryCount"] = s.RestartRetries
	}
	host["RestartPolicy"] = rp
	host["AutoRemove"] = s.AutoRemove

	if s.NetworkMode != "" {
		host["NetworkMode"] = s.NetworkMode
	}
	host["Dns"] = s.DNS
	host["ExtraHosts"] = s.ExtraHosts
	host["Privileged"] = s.Privileged
	host["ReadonlyRootfs"] = s.ReadOnlyRootfs
	host["CapAdd"] = s.CapAdd
	host["CapDrop"] = s.CapDrop
	host["GroupAdd"] = s.GroupAdd
	host["SecurityOpt"] = s.SecurityOpt
	host["Memory"] = s.Memory
	host["MemorySwap"] = s.MemorySwap
	host["NanoCpus"] = s.NanoCPUs
	host["PidsLimit"] = s.PidsLimit
	if s.CPUShares > 0 {
		host["CpuShares"] = s.CPUShares
	}
	if s.ShmSize > 0 {
		host["ShmSize"] = s.ShmSize
	}
	if s.Sysctls != nil {
		host["Sysctls"] = s.Sysctls
	}
	if s.Runtime != "" {
		host["Runtime"] = s.Runtime
	}

	devices := []any{}
	for _, d := range s.Devices {
		parts := strings.Split(d, ":")
		if parts[0] == "" {
			continue
		}
		dev := map[string]any{"PathOnHost": parts[0], "PathInContainer": parts[0], "CgroupPermissions": "rwm"}
		if len(parts) > 1 && parts[1] != "" {
			dev["PathInContainer"] = parts[1]
		}
		if len(parts) > 2 && parts[2] != "" {
			dev["CgroupPermissions"] = parts[2]
		}
		devices = append(devices, dev)
	}
	host["Devices"] = devices

	if s.LogDriver != "" {
		lc := map[string]any{"Type": s.LogDriver}
		if len(s.LogOptions) > 0 {
			lc["Config"] = s.LogOptions
		} else {
			lc["Config"] = map[string]string{}
		}
		host["LogConfig"] = lc
	}
}

func portKey(p PortMapping) string {
	proto := p.Protocol
	if proto == "" {
		proto = "tcp"
	}
	return strconv.Itoa(p.ContainerPort) + "/" + proto
}

// endpointsFrom rebuilds the NetworkingConfig for a recreate, preserving static
// addresses and aliases. The Engine accepts at most one endpoint at create
// time, so extra networks are reconnected afterwards by the caller.
func endpointsFrom(inspect, host map[string]any) map[string]any {
	mode := strAt(host, "NetworkMode")
	switch {
	case mode == "host", mode == "none", strings.HasPrefix(mode, "container:"):
		return nil
	}
	ns := mapAt(inspect, "NetworkSettings")
	networks := mapAt(ns, "Networks")
	if len(networks) == 0 {
		return nil
	}
	name := PrimaryNetwork(inspect)
	raw := mapAt(networks, name)
	if raw == nil {
		return nil
	}
	ep := map[string]any{}
	if ipam := mapAt(raw, "IPAMConfig"); ipam != nil {
		clean := map[string]any{}
		if v := strAt(ipam, "IPv4Address"); v != "" {
			clean["IPv4Address"] = v
		}
		if v := strAt(ipam, "IPv6Address"); v != "" {
			clean["IPv6Address"] = v
		}
		if len(clean) > 0 {
			ep["IPAMConfig"] = clean
		}
	}
	sid := shortID(strAt(inspect, "Id"))
	aliases := []string{}
	for _, a := range toStrSlice(raw["Aliases"]) {
		if a != "" && a != sid {
			aliases = append(aliases, a)
		}
	}
	if len(aliases) > 0 {
		ep["Aliases"] = aliases
	}
	return map[string]any{"EndpointsConfig": map[string]any{name: ep}}
}

// PrimaryNetwork picks the network a recreate should attach at create time:
// the one named by NetworkMode when possible, otherwise the first by name.
func PrimaryNetwork(inspect map[string]any) string {
	host := mapAt(inspect, "HostConfig")
	networks := mapAt(mapAt(inspect, "NetworkSettings"), "Networks")
	mode := strAt(host, "NetworkMode")
	if mode == "default" {
		mode = "bridge"
	}
	if _, ok := networks[mode]; ok {
		return mode
	}
	names := make([]string, 0, len(networks))
	for n := range networks {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// NetworkAttachments lists the networks a container is attached to, with the
// static address requested for each (empty when dynamic).
func NetworkAttachments(inspect map[string]any) map[string]EndpointSettings {
	out := map[string]EndpointSettings{}
	networks := mapAt(mapAt(inspect, "NetworkSettings"), "Networks")
	for name, raw := range networks {
		rm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ep := EndpointSettings{
			NetworkID:   strAt(rm, "NetworkID"),
			EndpointID:  strAt(rm, "EndpointID"),
			Gateway:     strAt(rm, "Gateway"),
			IPAddress:   strAt(rm, "IPAddress"),
			IPPrefixLen: int(i64At(rm, "IPPrefixLen")),
			MacAddress:  strAt(rm, "MacAddress"),
			Aliases:     toStrSlice(rm["Aliases"]),
		}
		if ipam := mapAt(rm, "IPAMConfig"); ipam != nil {
			ep.IPAMConfig = &EndpointIPAM{
				IPv4Address: strAt(ipam, "IPv4Address"),
				IPv6Address: strAt(ipam, "IPv6Address"),
			}
		}
		out[name] = ep
	}
	return out
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// ImageDefaults describes what an image expects, used to pre-fill the deploy form.
type ImageDefaults struct {
	Image        string            `json:"image"`
	Entrypoint   []string          `json:"entrypoint"`
	Cmd          []string          `json:"cmd"`
	Env          []string          `json:"env"`
	Labels       map[string]string `json:"labels"`
	WorkingDir   string            `json:"working_dir"`
	User         string            `json:"user"`
	Ports        []PortMapping     `json:"ports"`
	Volumes      []string          `json:"volumes"`
	StopSignal   string            `json:"stop_signal"`
	Healthcheck  bool              `json:"healthcheck"`
	Architecture string            `json:"architecture"`
	OS           string            `json:"os"`
	Size         int64             `json:"size"`
}

// DefaultsFromImage reads an image inspect document into deploy-form defaults.
func DefaultsFromImage(inspect map[string]any) *ImageDefaults {
	cfg := mapAt(inspect, "Config")
	d := &ImageDefaults{
		Entrypoint:   toStrSlice(cfg["Entrypoint"]),
		Cmd:          toStrSlice(cfg["Cmd"]),
		Env:          toStrSlice(cfg["Env"]),
		Labels:       toStrMap(cfg["Labels"]),
		WorkingDir:   strAt(cfg, "WorkingDir"),
		User:         strAt(cfg, "User"),
		StopSignal:   strAt(cfg, "StopSignal"),
		Architecture: strAt(inspect, "Architecture"),
		OS:           strAt(inspect, "Os"),
		Size:         i64At(inspect, "Size"),
		Ports:        []PortMapping{},
		Volumes:      []string{},
	}
	if mapAt(cfg, "Healthcheck") != nil {
		d.Healthcheck = true
	}
	for key := range mapAt(cfg, "ExposedPorts") {
		port, proto := splitPortProto(key)
		if port > 0 {
			d.Ports = append(d.Ports, PortMapping{ContainerPort: port, Protocol: proto})
		}
	}
	sort.Slice(d.Ports, func(i, j int) bool { return d.Ports[i].ContainerPort < d.Ports[j].ContainerPort })
	for target := range mapAt(cfg, "Volumes") {
		d.Volumes = append(d.Volumes, target)
	}
	sort.Strings(d.Volumes)
	if tags := toStrSlice(inspect["RepoTags"]); len(tags) > 0 {
		d.Image = tags[0]
	} else {
		d.Image = strAt(inspect, "Id")
	}
	return d
}
