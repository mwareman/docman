package dock

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ---------- shared types ----------

// Port is a published or exposed port on a container summary.
type Port struct {
	IP          string `json:"IP,omitempty"`
	PrivatePort int    `json:"PrivatePort"`
	PublicPort  int    `json:"PublicPort,omitempty"`
	Type        string `json:"Type"`
}

// MountPoint is a resolved mount on a running container.
type MountPoint struct {
	Type        string `json:"Type"`
	Name        string `json:"Name,omitempty"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	Mode        string `json:"Mode"`
	RW          bool   `json:"RW"`
	Propagation string `json:"Propagation,omitempty"`
}

// EndpointIPAM carries a requested static address for a network endpoint.
type EndpointIPAM struct {
	IPv4Address string `json:"IPv4Address,omitempty"`
	IPv6Address string `json:"IPv6Address,omitempty"`
}

// EndpointSettings is a container's attachment to one network.
type EndpointSettings struct {
	IPAMConfig  *EndpointIPAM `json:"IPAMConfig,omitempty"`
	Aliases     []string      `json:"Aliases,omitempty"`
	NetworkID   string        `json:"NetworkID,omitempty"`
	EndpointID  string        `json:"EndpointID,omitempty"`
	Gateway     string        `json:"Gateway,omitempty"`
	IPAddress   string        `json:"IPAddress,omitempty"`
	IPPrefixLen int           `json:"IPPrefixLen,omitempty"`
	MacAddress  string        `json:"MacAddress,omitempty"`
}

// ContainerSummary is one row of GET /containers/json.
type ContainerSummary struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	Command string            `json:"Command"`
	Created int64             `json:"Created"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Ports   []Port            `json:"Ports"`
	Labels  map[string]string `json:"Labels"`
	Mounts  []MountPoint      `json:"Mounts"`

	NetworkSettings struct {
		Networks map[string]EndpointSettings `json:"Networks"`
	} `json:"NetworkSettings"`

	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
}

// Name returns the primary container name without the leading slash.
func (c *ContainerSummary) Name() string {
	if len(c.Names) == 0 {
		return ""
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

// ImageSummary is one row of GET /images/json.
type ImageSummary struct {
	ID          string            `json:"Id"`
	ParentID    string            `json:"ParentId"`
	RepoTags    []string          `json:"RepoTags"`
	RepoDigests []string          `json:"RepoDigests"`
	Created     int64             `json:"Created"`
	Size        int64             `json:"Size"`
	SharedSize  int64             `json:"SharedSize"`
	Containers  int64             `json:"Containers"`
	Labels      map[string]string `json:"Labels"`
}

// ImageHistoryEntry is one layer in an image's history.
type ImageHistoryEntry struct {
	ID        string   `json:"Id"`
	Created   int64    `json:"Created"`
	CreatedBy string   `json:"CreatedBy"`
	Tags      []string `json:"Tags"`
	Size      int64    `json:"Size"`
	Comment   string   `json:"Comment"`
}

// VolumeUsage is optional volume size accounting.
type VolumeUsage struct {
	Size     int64 `json:"Size"`
	RefCount int64 `json:"RefCount"`
}

// Volume is a docker volume.
type Volume struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Mountpoint string            `json:"Mountpoint"`
	CreatedAt  string            `json:"CreatedAt,omitempty"`
	Labels     map[string]string `json:"Labels"`
	Options    map[string]string `json:"Options"`
	Scope      string            `json:"Scope"`
	UsageData  *VolumeUsage      `json:"UsageData,omitempty"`
}

// IPAMConfig is one subnet definition on a network.
type IPAMConfig struct {
	Subnet     string            `json:"Subnet,omitempty"`
	IPRange    string            `json:"IPRange,omitempty"`
	Gateway    string            `json:"Gateway,omitempty"`
	AuxAddress map[string]string `json:"AuxiliaryAddresses,omitempty"`
}

// NetworkContainer is a container attached to a network, as reported by inspect.
type NetworkContainer struct {
	Name        string `json:"Name"`
	EndpointID  string `json:"EndpointID"`
	MacAddress  string `json:"MacAddress"`
	IPv4Address string `json:"IPv4Address"`
	IPv6Address string `json:"IPv6Address"`
}

// Network is a docker network.
type Network struct {
	Name       string `json:"Name"`
	ID         string `json:"Id"`
	Created    string `json:"Created,omitempty"`
	Scope      string `json:"Scope"`
	Driver     string `json:"Driver"`
	EnableIPv6 bool   `json:"EnableIPv6"`
	Internal   bool   `json:"Internal"`
	Attachable bool   `json:"Attachable"`
	IPAM       struct {
		Driver  string            `json:"Driver"`
		Options map[string]string `json:"Options"`
		Config  []IPAMConfig      `json:"Config"`
	} `json:"IPAM"`
	Containers map[string]NetworkContainer `json:"Containers"`
	Options    map[string]string           `json:"Options"`
	Labels     map[string]string           `json:"Labels"`
}

// Info is the subset of GET /info DocMan surfaces.
type Info struct {
	ID                string `json:"ID"`
	Name              string `json:"Name"`
	ServerVersion     string `json:"ServerVersion"`
	OperatingSystem   string `json:"OperatingSystem"`
	OSType            string `json:"OSType"`
	Architecture      string `json:"Architecture"`
	KernelVersion     string `json:"KernelVersion"`
	NCPU              int    `json:"NCPU"`
	MemTotal          int64  `json:"MemTotal"`
	Containers        int    `json:"Containers"`
	ContainersRunning int    `json:"ContainersRunning"`
	ContainersPaused  int    `json:"ContainersPaused"`
	ContainersStopped int    `json:"ContainersStopped"`
	Images            int    `json:"Images"`
	Driver            string `json:"Driver"`
	CgroupDriver      string `json:"CgroupDriver"`
	CgroupVersion     string `json:"CgroupVersion"`
	DockerRootDir     string `json:"DockerRootDir"`
	Plugins           struct {
		// Volume lists the volume drivers the Engine can use: "local" plus
		// any enabled volume plugins.
		Volume []string `json:"Volume"`
	} `json:"Plugins"`
}

// Version is the subset of GET /version DocMan surfaces.
type Version struct {
	Version       string `json:"Version"`
	APIVersion    string `json:"ApiVersion"`
	MinAPIVersion string `json:"MinAPIVersion"`
	GitCommit     string `json:"GitCommit"`
	GoVersion     string `json:"GoVersion"`
	Os            string `json:"Os"`
	Arch          string `json:"Arch"`
	KernelVersion string `json:"KernelVersion"`
}

// DiskUsage is GET /system/df.
type DiskUsage struct {
	LayersSize int64           `json:"LayersSize"`
	Images     []*ImageSummary `json:"Images"`
	Volumes    []*Volume       `json:"Volumes"`
}

// CPUStats is the cgroup CPU accounting block of a stats sample.
type CPUStats struct {
	CPUUsage struct {
		TotalUsage        uint64   `json:"total_usage"`
		PercpuUsage       []uint64 `json:"percpu_usage"`
		UsageInKernelmode uint64   `json:"usage_in_kernelmode"`
		UsageInUsermode   uint64   `json:"usage_in_usermode"`
	} `json:"cpu_usage"`
	SystemUsage    uint64 `json:"system_cpu_usage"`
	OnlineCPUs     uint32 `json:"online_cpus"`
	ThrottlingData struct {
		Periods          uint64 `json:"periods"`
		ThrottledPeriods uint64 `json:"throttled_periods"`
		ThrottledTime    uint64 `json:"throttled_time"`
	} `json:"throttling_data"`
}

// MemoryStats is the cgroup memory accounting block of a stats sample.
type MemoryStats struct {
	Usage    uint64            `json:"usage"`
	MaxUsage uint64            `json:"max_usage"`
	Limit    uint64            `json:"limit"`
	Stats    map[string]uint64 `json:"stats"`
}

// BlkioEntry is one block-I/O counter.
type BlkioEntry struct {
	Major uint64 `json:"major"`
	Minor uint64 `json:"minor"`
	Op    string `json:"op"`
	Value uint64 `json:"value"`
}

// BlkioStats is the block-I/O accounting block of a stats sample.
type BlkioStats struct {
	IoServiceBytesRecursive []BlkioEntry `json:"io_service_bytes_recursive"`
	IoServicedRecursive     []BlkioEntry `json:"io_serviced_recursive"`
}

// NetworkStats is per-interface network accounting.
type NetworkStats struct {
	RxBytes   uint64 `json:"rx_bytes"`
	RxPackets uint64 `json:"rx_packets"`
	RxErrors  uint64 `json:"rx_errors"`
	RxDropped uint64 `json:"rx_dropped"`
	TxBytes   uint64 `json:"tx_bytes"`
	TxPackets uint64 `json:"tx_packets"`
	TxErrors  uint64 `json:"tx_errors"`
	TxDropped uint64 `json:"tx_dropped"`
}

// Stats is one sample from GET /containers/{id}/stats.
type Stats struct {
	Read        string                  `json:"read"`
	PreRead     string                  `json:"preread"`
	Name        string                  `json:"name"`
	ID          string                  `json:"id"`
	CPUStats    CPUStats                `json:"cpu_stats"`
	PreCPUStats CPUStats                `json:"precpu_stats"`
	MemoryStats MemoryStats             `json:"memory_stats"`
	BlkioStats  BlkioStats              `json:"blkio_stats"`
	Networks    map[string]NetworkStats `json:"networks"`
	PidsStats   struct {
		Current uint64 `json:"current"`
		Limit   uint64 `json:"limit"`
	} `json:"pids_stats"`
}

// CreateResponse is returned by container create.
type CreateResponse struct {
	ID       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

// ExecCreateResponse is returned by exec create.
type ExecCreateResponse struct {
	ID string `json:"Id"`
}

// PruneReport is the common shape of the prune endpoints.
type PruneReport struct {
	ContainersDeleted []string `json:"ContainersDeleted,omitempty"`
	ImagesDeleted     []struct {
		Untagged string `json:"Untagged,omitempty"`
		Deleted  string `json:"Deleted,omitempty"`
	} `json:"ImagesDeleted,omitempty"`
	VolumesDeleted  []string `json:"VolumesDeleted,omitempty"`
	NetworksDeleted []string `json:"NetworksDeleted,omitempty"`
	SpaceReclaimed  int64    `json:"SpaceReclaimed"`
}

// ---------- system ----------

// Ping checks that the Engine is reachable.
func (c *Client) Ping(ctx context.Context) error {
	return c.Do(ctx, http.MethodGet, "/_ping", nil, nil, nil)
}

// Info returns engine information.
func (c *Client) Info(ctx context.Context) (*Info, error) {
	var out Info
	if err := c.Get(ctx, "/info", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Version returns engine version details.
func (c *Client) Version(ctx context.Context) (*Version, error) {
	var out Version
	if err := c.Get(ctx, "/version", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DiskUsage returns storage accounting for images and volumes.
func (c *Client) DiskUsage(ctx context.Context) (*DiskUsage, error) {
	var out DiskUsage
	if err := c.Get(ctx, "/system/df", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Events opens the engine event stream.
func (c *Client) Events(ctx context.Context) (io.ReadCloser, error) {
	return c.Stream(ctx, http.MethodGet, "/events", nil, nil, "")
}

// ---------- containers ----------

// ListContainers returns container summaries; all includes stopped containers.
func (c *Client) ListContainers(ctx context.Context, all bool) ([]*ContainerSummary, error) {
	q := url.Values{}
	if all {
		q.Set("all", "1")
	}
	out := []*ContainerSummary{}
	if err := c.Get(ctx, "/containers/json", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// InspectContainer returns the full inspect document as a generic map so that
// fields DocMan does not model survive a recreate untouched.
func (c *Client) InspectContainer(ctx context.Context, id string) (map[string]any, error) {
	out := map[string]any{}
	if err := c.Get(ctx, "/containers/"+id+"/json", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateContainer creates a container from a raw create body.
func (c *Client) CreateContainer(ctx context.Context, name string, body map[string]any) (*CreateResponse, error) {
	q := url.Values{}
	if name != "" {
		q.Set("name", name)
	}
	var out CreateResponse
	if err := c.Post(ctx, "/containers/create", q, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StartContainer starts a container.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.Post(ctx, "/containers/"+id+"/start", nil, nil, nil)
}

// StopContainer stops a container, waiting up to timeout seconds.
func (c *Client) StopContainer(ctx context.Context, id string, timeout int) error {
	q := url.Values{}
	if timeout >= 0 {
		q.Set("t", strconv.Itoa(timeout))
	}
	return c.Post(ctx, "/containers/"+id+"/stop", q, nil, nil)
}

// RestartContainer restarts a container.
func (c *Client) RestartContainer(ctx context.Context, id string, timeout int) error {
	q := url.Values{}
	if timeout >= 0 {
		q.Set("t", strconv.Itoa(timeout))
	}
	return c.Post(ctx, "/containers/"+id+"/restart", q, nil, nil)
}

// KillContainer sends a signal to a container.
func (c *Client) KillContainer(ctx context.Context, id, signal string) error {
	q := url.Values{}
	if signal != "" {
		q.Set("signal", signal)
	}
	return c.Post(ctx, "/containers/"+id+"/kill", q, nil, nil)
}

// PauseContainer freezes a container's processes.
func (c *Client) PauseContainer(ctx context.Context, id string) error {
	return c.Post(ctx, "/containers/"+id+"/pause", nil, nil, nil)
}

// UnpauseContainer resumes a paused container.
func (c *Client) UnpauseContainer(ctx context.Context, id string) error {
	return c.Post(ctx, "/containers/"+id+"/unpause", nil, nil, nil)
}

// RenameContainer renames a container.
func (c *Client) RenameContainer(ctx context.Context, id, name string) error {
	q := url.Values{"name": {name}}
	return c.Post(ctx, "/containers/"+id+"/rename", q, nil, nil)
}

// RemoveContainer deletes a container, optionally with its anonymous volumes.
func (c *Client) RemoveContainer(ctx context.Context, id string, force, volumes bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	if volumes {
		q.Set("v", "1")
	}
	return c.Delete(ctx, "/containers/"+id, q)
}

// UpdateContainer applies live resource limit changes.
func (c *Client) UpdateContainer(ctx context.Context, id string, body map[string]any) error {
	return c.Post(ctx, "/containers/"+id+"/update", nil, body, nil)
}

// LogOptions controls a container log request.
type LogOptions struct {
	Follow     bool
	Stdout     bool
	Stderr     bool
	Timestamps bool
	Tail       string
	Since      string
}

// ContainerLogs opens a container log stream. When the container has no TTY the
// stream is 8-byte framed; use ReadFrame to demultiplex it.
func (c *Client) ContainerLogs(ctx context.Context, id string, opt LogOptions) (io.ReadCloser, error) {
	q := url.Values{}
	q.Set("stdout", boolParam(opt.Stdout))
	q.Set("stderr", boolParam(opt.Stderr))
	q.Set("follow", boolParam(opt.Follow))
	q.Set("timestamps", boolParam(opt.Timestamps))
	if opt.Tail != "" {
		q.Set("tail", opt.Tail)
	}
	if opt.Since != "" {
		q.Set("since", opt.Since)
	}
	return c.Stream(ctx, http.MethodGet, "/containers/"+id+"/logs", q, nil, "")
}

// ContainerStatsStream opens a live stats stream for one container.
func (c *Client) ContainerStatsStream(ctx context.Context, id string) (io.ReadCloser, error) {
	q := url.Values{"stream": {"1"}}
	return c.Stream(ctx, http.MethodGet, "/containers/"+id+"/stats", q, nil, "")
}

// ContainerStatsOnce returns a single stats sample.
func (c *Client) ContainerStatsOnce(ctx context.Context, id string) (*Stats, error) {
	q := url.Values{"stream": {"0"}}
	var out Stats
	if err := c.Get(ctx, "/containers/"+id+"/stats", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ContainerTop lists processes running inside a container.
func (c *Client) ContainerTop(ctx context.Context, id, psArgs string) (map[string]any, error) {
	q := url.Values{}
	if psArgs != "" {
		q.Set("ps_args", psArgs)
	}
	out := map[string]any{}
	if err := c.Get(ctx, "/containers/"+id+"/top", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FilesystemChange is one entry from the container diff endpoint.
type FilesystemChange struct {
	Path string `json:"Path"`
	Kind int    `json:"Kind"`
}

// ContainerChanges lists filesystem changes relative to the image.
func (c *Client) ContainerChanges(ctx context.Context, id string) ([]FilesystemChange, error) {
	out := []FilesystemChange{}
	if err := c.Get(ctx, "/containers/"+id+"/changes", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- exec ----------

// ExecConfig configures an exec session.
type ExecConfig struct {
	AttachStdin  bool     `json:"AttachStdin"`
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
	Tty          bool     `json:"Tty"`
	Cmd          []string `json:"Cmd"`
	Env          []string `json:"Env,omitempty"`
	User         string   `json:"User,omitempty"`
	WorkingDir   string   `json:"WorkingDir,omitempty"`
	Privileged   bool     `json:"Privileged,omitempty"`
}

// ExecCreate creates an exec instance in a container.
func (c *Client) ExecCreate(ctx context.Context, id string, cfg ExecConfig) (string, error) {
	var out ExecCreateResponse
	if err := c.Post(ctx, "/containers/"+id+"/exec", nil, cfg, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// ExecStart attaches to an exec instance, returning the raw duplex stream.
func (c *Client) ExecStart(ctx context.Context, execID string, tty bool) (*Hijacked, error) {
	body := map[string]any{"Detach": false, "Tty": tty}
	return c.Hijack(ctx, "/exec/"+execID+"/start", nil, body)
}

// ExecResize tells the Engine the new terminal dimensions.
func (c *Client) ExecResize(ctx context.Context, execID string, rows, cols int) error {
	q := url.Values{"h": {strconv.Itoa(rows)}, "w": {strconv.Itoa(cols)}}
	return c.Post(ctx, "/exec/"+execID+"/resize", q, nil, nil)
}

// ExecInspect returns exec state, including the exit code once it has finished.
func (c *Client) ExecInspect(ctx context.Context, execID string) (map[string]any, error) {
	out := map[string]any{}
	if err := c.Get(ctx, "/exec/"+execID+"/json", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RunExec runs a command in a running container and collects what it prints.
// Output beyond limit bytes on either stream is an error, so a runaway
// command cannot exhaust memory.
func (c *Client) RunExec(ctx context.Context, id string, cmd []string, limit int) (stdout, stderr []byte, code int, err error) {
	execID, err := c.ExecCreate(ctx, id, ExecConfig{AttachStdout: true, AttachStderr: true, Cmd: cmd})
	if err != nil {
		return nil, nil, -1, err
	}
	hj, err := c.ExecStart(ctx, execID, false)
	if err != nil {
		return nil, nil, -1, err
	}
	defer hj.Close()
	var out, errOut bytes.Buffer
	for {
		frame, ferr := ReadFrame(hj.Reader)
		if ferr != nil {
			break
		}
		switch frame.Stream {
		case 2:
			errOut.Write(frame.Data)
		default:
			out.Write(frame.Data)
		}
		if out.Len() > limit || errOut.Len() > limit {
			return nil, nil, -1, fmt.Errorf("command output exceeded %d bytes", limit)
		}
	}
	info, err := c.ExecInspect(ctx, execID)
	if err != nil {
		return out.Bytes(), errOut.Bytes(), -1, err
	}
	if n, ok := info["ExitCode"].(float64); ok {
		code = int(n)
	}
	return out.Bytes(), errOut.Bytes(), code, nil
}

// GetArchive streams a tar of a path inside a container: one entry for a
// file, the whole tree for a directory. It works on stopped containers too.
func (c *Client) GetArchive(ctx context.Context, id, path string) (io.ReadCloser, error) {
	return c.Stream(ctx, http.MethodGet, "/containers/"+id+"/archive", url.Values{"path": {path}}, nil, "")
}

// PutArchive extracts a tar stream into a directory inside a container.
func (c *Client) PutArchive(ctx context.Context, id, dir string, tarball io.Reader) error {
	body, err := c.Stream(ctx, http.MethodPut, "/containers/"+id+"/archive",
		url.Values{"path": {dir}, "noOverwriteDirNonDir": {"true"}}, tarball, "application/x-tar")
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, body)
	return body.Close()
}

// ---------- images ----------

// ListImages returns image summaries.
func (c *Client) ListImages(ctx context.Context, all bool) ([]*ImageSummary, error) {
	q := url.Values{}
	if all {
		q.Set("all", "1")
	}
	out := []*ImageSummary{}
	if err := c.Get(ctx, "/images/json", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// InspectImage returns the full image inspect document.
func (c *Client) InspectImage(ctx context.Context, name string) (map[string]any, error) {
	out := map[string]any{}
	if err := c.Get(ctx, "/images/"+name+"/json", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RegistryDigest asks the image's registry, through the Engine, for the digest
// its tag points at now. Nothing is downloaded; the Engine uses its own proxy
// and certificate settings, so this works wherever a pull would.
func (c *Client) RegistryDigest(ctx context.Context, ref string, auth *RegistryAuth) (string, error) {
	var out struct {
		Descriptor struct {
			Digest string `json:"digest"`
		} `json:"Descriptor"`
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/distribution/"+ref+"/json", nil, nil, "")
	if err != nil {
		return "", err
	}
	if h := auth.header(); h != "" {
		req.Header.Set("X-Registry-Auth", h)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", decodeError(resp)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Descriptor.Digest == "" {
		return "", errors.New("the registry returned no digest")
	}
	return out.Descriptor.Digest, nil
}

// ImageHistory returns an image's layer history.
func (c *Client) ImageHistory(ctx context.Context, name string) ([]ImageHistoryEntry, error) {
	out := []ImageHistoryEntry{}
	if err := c.Get(ctx, "/images/"+name+"/history", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RegistryAuth is the credential blob sent in X-Registry-Auth: a username and
// password (or access token), or an identity token some registries issue.
type RegistryAuth struct {
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	IdentityToken string `json:"identitytoken,omitempty"`
	ServerAddress string `json:"serveraddress,omitempty"`
}

func (a *RegistryAuth) header() string {
	if a == nil || (a.Username == "" && a.Password == "" && a.IdentityToken == "") {
		return ""
	}
	buf, err := json.Marshal(a)
	if err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(buf)
}

// CheckAuth has the Engine sign in to a registry with the given credentials,
// the way `docker login` does, without storing anything.
func (c *Client) CheckAuth(ctx context.Context, auth *RegistryAuth) (string, error) {
	var out struct {
		Status string `json:"Status"`
	}
	if err := c.Post(ctx, "/auth", nil, auth, &out); err != nil {
		return "", err
	}
	return out.Status, nil
}

// SearchResult is one Docker Hub search hit.
type SearchResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	StarCount   int    `json:"star_count"`
	IsOfficial  bool   `json:"is_official"`
}

// SearchImages searches Docker Hub through the Engine.
func (c *Client) SearchImages(ctx context.Context, term string, limit int) ([]SearchResult, error) {
	out := []SearchResult{}
	q := url.Values{"term": {term}, "limit": {strconv.Itoa(limit)}}
	if err := c.Get(ctx, "/images/search", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PullImage streams the progress of pulling image:tag.
func (c *Client) PullImage(ctx context.Context, image, tag string, auth *RegistryAuth) (io.ReadCloser, error) {
	q := url.Values{"fromImage": {image}}
	if tag != "" {
		q.Set("tag", tag)
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/images/create", q, nil, "application/json")
	if err != nil {
		return nil, err
	}
	if h := auth.header(); h != "" {
		req.Header.Set("X-Registry-Auth", h)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, decodeError(resp)
	}
	return resp.Body, nil
}

// LoadImage imports an image tarball, streaming import progress back.
func (c *Client) LoadImage(ctx context.Context, tarball io.Reader) (io.ReadCloser, error) {
	q := url.Values{"quiet": {"0"}}
	return c.Stream(ctx, http.MethodPost, "/images/load", q, tarball, "application/x-tar")
}

// TagImage adds repo:tag to an existing image.
func (c *Client) TagImage(ctx context.Context, name, repo, tag string) error {
	q := url.Values{"repo": {repo}}
	if tag != "" {
		q.Set("tag", tag)
	}
	return c.Post(ctx, "/images/"+name+"/tag", q, nil, nil)
}

// RemoveImage deletes an image or untags it.
func (c *Client) RemoveImage(ctx context.Context, name string, force, noprune bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	if noprune {
		q.Set("noprune", "1")
	}
	return c.Delete(ctx, "/images/"+name, q)
}

// PruneImages removes unused images. When dangling is false, all unused images go.
func (c *Client) PruneImages(ctx context.Context, dangling bool) (*PruneReport, error) {
	q := url.Values{}
	if !dangling {
		q.Set("filters", `{"dangling":{"false":true}}`)
	}
	var out PruneReport
	if err := c.Post(ctx, "/images/prune", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------- volumes ----------

// ListVolumes returns all volumes.
func (c *Client) ListVolumes(ctx context.Context) ([]*Volume, error) {
	var out struct {
		Volumes  []*Volume `json:"Volumes"`
		Warnings []string  `json:"Warnings"`
	}
	if err := c.Get(ctx, "/volumes", nil, &out); err != nil {
		return nil, err
	}
	if out.Volumes == nil {
		out.Volumes = []*Volume{}
	}
	return out.Volumes, nil
}

// InspectVolume returns one volume.
func (c *Client) InspectVolume(ctx context.Context, name string) (*Volume, error) {
	var out Volume
	if err := c.Get(ctx, "/volumes/"+name, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateVolume creates a named volume.
func (c *Client) CreateVolume(ctx context.Context, name, driver string, labels, options map[string]string) (*Volume, error) {
	if driver == "" {
		driver = "local"
	}
	body := map[string]any{"Name": name, "Driver": driver}
	if len(labels) > 0 {
		body["Labels"] = labels
	}
	if len(options) > 0 {
		body["DriverOpts"] = options
	}
	var out Volume
	if err := c.Post(ctx, "/volumes/create", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveVolume deletes a volume.
func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	return c.Delete(ctx, "/volumes/"+name, q)
}

// PruneVolumes removes unused volumes.
func (c *Client) PruneVolumes(ctx context.Context) (*PruneReport, error) {
	var out PruneReport
	if err := c.Post(ctx, "/volumes/prune", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------- networks ----------

// ListNetworks returns all networks.
func (c *Client) ListNetworks(ctx context.Context) ([]*Network, error) {
	out := []*Network{}
	if err := c.Get(ctx, "/networks", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// InspectNetwork returns one network including attached containers.
func (c *Client) InspectNetwork(ctx context.Context, id string) (*Network, error) {
	var out Network
	if err := c.Get(ctx, "/networks/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// NetworkIPAM is the address-management block of a network create request.
type NetworkIPAM struct {
	Driver string       `json:"Driver,omitempty"`
	Config []IPAMConfig `json:"Config,omitempty"`
}

// CreateNetworkRequest is the body of POST /networks/create.
type CreateNetworkRequest struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver,omitempty"`
	Attachable bool              `json:"Attachable,omitempty"`
	EnableIPv6 bool              `json:"EnableIPv6,omitempty"`
	Internal   bool              `json:"Internal,omitempty"`
	Options    map[string]string `json:"Options,omitempty"`
	Labels     map[string]string `json:"Labels,omitempty"`
	IPAM       *NetworkIPAM      `json:"IPAM,omitempty"`
}

// CreateNetwork creates a network and returns its id.
func (c *Client) CreateNetwork(ctx context.Context, req *CreateNetworkRequest) (string, error) {
	var out struct {
		ID      string `json:"Id"`
		Warning string `json:"Warning"`
	}
	if err := c.Post(ctx, "/networks/create", nil, req, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// RemoveNetwork deletes a network.
func (c *Client) RemoveNetwork(ctx context.Context, id string) error {
	return c.Delete(ctx, "/networks/"+id, nil)
}

// ConnectNetwork attaches a container to a network, optionally at a fixed address.
func (c *Client) ConnectNetwork(ctx context.Context, netID, container string, cfg *EndpointSettings) error {
	body := map[string]any{"Container": container}
	if cfg != nil {
		body["EndpointConfig"] = cfg
	}
	return c.Post(ctx, "/networks/"+netID+"/connect", nil, body, nil)
}

// DisconnectNetwork detaches a container from a network.
func (c *Client) DisconnectNetwork(ctx context.Context, netID, container string, force bool) error {
	body := map[string]any{"Container": container, "Force": force}
	return c.Post(ctx, "/networks/"+netID+"/disconnect", nil, body, nil)
}

func boolParam(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
