// Package hostmetrics reads host-level CPU, memory and block-I/O counters from
// procfs and turns successive readings into rates.
//
// procfs is not namespaced for /proc/stat, /proc/meminfo or /proc/diskstats, so
// a container sees the host's figures without any special mounts. Bind-mounting
// the host's /proc somewhere and pointing DOCMAN_PROC_PATH at it also works.
package hostmetrics

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// sectorSize is the fixed unit used by /proc/diskstats.
const sectorSize = 512

// CPUTimes holds jiffies from one /proc/stat cpu line.
type CPUTimes struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

// Total is the sum of every counter.
func (c CPUTimes) Total() uint64 {
	return c.User + c.Nice + c.System + c.Idle + c.IOWait + c.IRQ + c.SoftIRQ + c.Steal
}

// Busy is everything except idle and iowait.
func (c CPUTimes) Busy() uint64 {
	return c.User + c.Nice + c.System + c.IRQ + c.SoftIRQ + c.Steal
}

// MemInfo holds the memory figures DocMan displays, in bytes.
type MemInfo struct {
	Total     uint64 `json:"total"`
	Free      uint64 `json:"free"`
	Available uint64 `json:"available"`
	Buffers   uint64 `json:"buffers"`
	Cached    uint64 `json:"cached"`
	SwapTotal uint64 `json:"swap_total"`
	SwapFree  uint64 `json:"swap_free"`
}

// Used is total minus available, i.e. what applications actually hold.
func (m MemInfo) Used() uint64 {
	if m.Available > 0 && m.Total >= m.Available {
		return m.Total - m.Available
	}
	if m.Total >= m.Free+m.Buffers+m.Cached {
		return m.Total - m.Free - m.Buffers - m.Cached
	}
	return 0
}

// DiskCounters holds cumulative per-device I/O counters.
type DiskCounters struct {
	Name         string
	ReadOps      uint64
	ReadSectors  uint64
	WriteOps     uint64
	WriteSectors uint64
}

// DiskRate is a per-device I/O rate.
type DiskRate struct {
	Name       string  `json:"name"`
	ReadBytes  float64 `json:"read_bytes"`
	WriteBytes float64 `json:"write_bytes"`
	ReadOps    float64 `json:"read_ops"`
	WriteOps   float64 `json:"write_ops"`
}

type snapshot struct {
	at     time.Time
	cpu    CPUTimes
	perCPU []CPUTimes
	disks  map[string]DiskCounters
}

// Sample is one computed host metrics reading, ready to serialise to the UI.
type Sample struct {
	Timestamp int64 `json:"t"`
	NCPU      int   `json:"ncpu"`

	CPUPercent    float64   `json:"cpu_percent"`
	CPUUser       float64   `json:"cpu_user"`
	CPUSystem     float64   `json:"cpu_system"`
	CPUIOWait     float64   `json:"cpu_iowait"`
	CPUSteal      float64   `json:"cpu_steal"`
	PerCPUPercent []float64 `json:"per_cpu_percent"`

	Memory   MemInfo `json:"memory"`
	MemUsed  uint64  `json:"mem_used"`
	SwapUsed uint64  `json:"swap_used"`

	DiskReadBytes  float64    `json:"disk_read_bytes"`
	DiskWriteBytes float64    `json:"disk_write_bytes"`
	DiskReadOps    float64    `json:"disk_read_ops"`
	DiskWriteOps   float64    `json:"disk_write_ops"`
	Disks          []DiskRate `json:"disks"`

	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
	Uptime float64 `json:"uptime"`
}

// Reader samples procfs and derives rates from consecutive samples.
type Reader struct {
	root string

	mu   sync.Mutex
	prev *snapshot
}

// New builds a Reader. root defaults to /proc, or DOCMAN_PROC_PATH when set.
func New(root string) *Reader {
	if root == "" {
		root = os.Getenv("DOCMAN_PROC_PATH")
	}
	if root == "" {
		root = "/proc"
	}
	return &Reader{root: strings.TrimRight(root, "/")}
}

// Available reports whether procfs is readable at all, which it is not on
// non-Linux docker hosts reached over TCP.
func (r *Reader) Available() bool {
	if _, err := os.Stat(r.root + "/stat"); err != nil {
		return false
	}
	return true
}

// Sample takes a reading. The first call has no previous snapshot to compare
// against, so its rates are zero; callers poll on an interval.
func (r *Reader) Sample() *Sample {
	nowSnap := &snapshot{at: time.Now()}
	nowSnap.cpu, nowSnap.perCPU = r.readStat()
	nowSnap.disks = r.readDiskstats()

	mem := r.readMeminfo()
	l1, l5, l15 := r.readLoad()
	out := &Sample{
		Timestamp: nowSnap.at.UnixMilli(),
		NCPU:      len(nowSnap.perCPU),
		Memory:    mem,
		MemUsed:   mem.Used(),
		Load1:     l1,
		Load5:     l5,
		Load15:    l15,
		Uptime:    r.readUptime(),
		Disks:     []DiskRate{},
	}
	if mem.SwapTotal >= mem.SwapFree {
		out.SwapUsed = mem.SwapTotal - mem.SwapFree
	}
	if out.NCPU == 0 {
		out.NCPU = 1
	}

	r.mu.Lock()
	prev := r.prev
	r.prev = nowSnap
	r.mu.Unlock()

	if prev == nil {
		return out
	}
	elapsed := nowSnap.at.Sub(prev.at).Seconds()
	if elapsed <= 0 {
		return out
	}

	if dt := float64(nowSnap.cpu.Total() - prev.cpu.Total()); dt > 0 {
		out.CPUPercent = clampPct(float64(nowSnap.cpu.Busy()-prev.cpu.Busy()) / dt * 100)
		out.CPUUser = clampPct(float64((nowSnap.cpu.User+nowSnap.cpu.Nice)-(prev.cpu.User+prev.cpu.Nice)) / dt * 100)
		out.CPUSystem = clampPct(float64((nowSnap.cpu.System+nowSnap.cpu.IRQ+nowSnap.cpu.SoftIRQ)-(prev.cpu.System+prev.cpu.IRQ+prev.cpu.SoftIRQ)) / dt * 100)
		out.CPUIOWait = clampPct(float64(nowSnap.cpu.IOWait-prev.cpu.IOWait) / dt * 100)
		out.CPUSteal = clampPct(float64(nowSnap.cpu.Steal-prev.cpu.Steal) / dt * 100)
	}

	out.PerCPUPercent = make([]float64, 0, len(nowSnap.perCPU))
	for i, cur := range nowSnap.perCPU {
		if i >= len(prev.perCPU) {
			out.PerCPUPercent = append(out.PerCPUPercent, 0)
			continue
		}
		dt := float64(cur.Total() - prev.perCPU[i].Total())
		if dt <= 0 {
			out.PerCPUPercent = append(out.PerCPUPercent, 0)
			continue
		}
		out.PerCPUPercent = append(out.PerCPUPercent, clampPct(float64(cur.Busy()-prev.perCPU[i].Busy())/dt*100))
	}

	for name, cur := range nowSnap.disks {
		old, ok := prev.disks[name]
		if !ok {
			continue
		}
		rate := DiskRate{
			Name:       name,
			ReadBytes:  perSecond(cur.ReadSectors, old.ReadSectors, elapsed) * sectorSize,
			WriteBytes: perSecond(cur.WriteSectors, old.WriteSectors, elapsed) * sectorSize,
			ReadOps:    perSecond(cur.ReadOps, old.ReadOps, elapsed),
			WriteOps:   perSecond(cur.WriteOps, old.WriteOps, elapsed),
		}
		out.DiskReadBytes += rate.ReadBytes
		out.DiskWriteBytes += rate.WriteBytes
		out.DiskReadOps += rate.ReadOps
		out.DiskWriteOps += rate.WriteOps
		if rate.ReadBytes > 0 || rate.WriteBytes > 0 {
			out.Disks = append(out.Disks, rate)
		}
	}
	return out
}

func perSecond(cur, old uint64, elapsed float64) float64 {
	if cur < old || elapsed <= 0 {
		return 0
	}
	return float64(cur-old) / elapsed
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func (r *Reader) readStat() (CPUTimes, []CPUTimes) {
	var agg CPUTimes
	var per []CPUTimes
	r.eachLine("/stat", func(line string) bool {
		if !strings.HasPrefix(line, "cpu") {
			return false // cpu lines come first; stop at the first non-cpu line
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			return true
		}
		t := CPUTimes{
			User:    parseUint(fields[1]),
			Nice:    parseUint(fields[2]),
			System:  parseUint(fields[3]),
			Idle:    parseUint(fields[4]),
			IOWait:  atIdx(fields, 5),
			IRQ:     atIdx(fields, 6),
			SoftIRQ: atIdx(fields, 7),
			Steal:   atIdx(fields, 8),
		}
		if fields[0] == "cpu" {
			agg = t
		} else {
			per = append(per, t)
		}
		return true
	})
	return agg, per
}

func (r *Reader) readMeminfo() MemInfo {
	var m MemInfo
	r.eachLine("/meminfo", func(line string) bool {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return true
		}
		parts := strings.Fields(strings.TrimSpace(value))
		if len(parts) == 0 {
			return true
		}
		// Values are in kibibytes.
		kb := parseUint(parts[0])
		switch key {
		case "MemTotal":
			m.Total = kb * 1024
		case "MemFree":
			m.Free = kb * 1024
		case "MemAvailable":
			m.Available = kb * 1024
		case "Buffers":
			m.Buffers = kb * 1024
		case "Cached":
			m.Cached = kb * 1024
		case "SwapTotal":
			m.SwapTotal = kb * 1024
		case "SwapFree":
			m.SwapFree = kb * 1024
		}
		return true
	})
	return m
}

func (r *Reader) readDiskstats() map[string]DiskCounters {
	out := map[string]DiskCounters{}
	r.eachLine("/diskstats", func(line string) bool {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return true
		}
		name := fields[2]
		if !isPhysicalDevice(name) {
			return true
		}
		out[name] = DiskCounters{
			Name:         name,
			ReadOps:      parseUint(fields[3]),
			ReadSectors:  parseUint(fields[5]),
			WriteOps:     parseUint(fields[7]),
			WriteSectors: parseUint(fields[9]),
		}
		return true
	})
	return out
}

// isPhysicalDevice keeps whole disks and drops partitions and pseudo devices,
// so totals are not double counted.
func isPhysicalDevice(name string) bool {
	for _, skip := range []string{"loop", "ram", "zram", "sr", "fd", "dm-", "md"} {
		if strings.HasPrefix(name, skip) {
			return false
		}
	}
	// nvme0n1p3 / mmcblk0p2 style partitions.
	if strings.HasPrefix(name, "nvme") || strings.HasPrefix(name, "mmcblk") {
		if i := strings.LastIndexByte(name, 'p'); i > 0 && allDigits(name[i+1:]) {
			return false
		}
		return true
	}
	// sda1 / vdb2 / hda3 style partitions.
	if len(name) > 0 && allDigits(name[len(name)-1:]) {
		return false
	}
	return true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (r *Reader) readLoad() (float64, float64, float64) {
	raw, err := os.ReadFile(r.root + "/loadavg")
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	return parseFloat(fields[0]), parseFloat(fields[1]), parseFloat(fields[2])
}

func (r *Reader) readUptime() float64 {
	raw, err := os.ReadFile(r.root + "/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0
	}
	return parseFloat(fields[0])
}

// eachLine walks a procfs file; returning false from fn stops the walk.
func (r *Reader) eachLine(name string, fn func(line string) bool) {
	f, err := os.Open(r.root + name)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if !fn(sc.Text()) {
			return
		}
	}
}

func atIdx(fields []string, i int) uint64 {
	if i >= len(fields) {
		return 0
	}
	return parseUint(fields[i])
}

func parseUint(s string) uint64 {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func parseFloat(s string) float64 {
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return n
}
