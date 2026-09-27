package srv

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"docman/internal/dock"
)

// volumeView adds "who is using this" to a docker volume.
type volumeView struct {
	*dock.Volume
	UsedBy []volumeUse `json:"used_by"`
}

type volumeUse struct {
	Container string `json:"container"`
	Target    string `json:"target"`
	ReadOnly  bool   `json:"read_only"`
	State     string `json:"state"`
}

func (s *Server) handleListVolumes(w http.ResponseWriter, r *http.Request) {
	volumes, err := s.dc.ListVolumes(r.Context())
	if err != nil {
		failDocker(w, err)
		return
	}
	usage := map[string][]volumeUse{}
	if containers, err := s.dc.ListContainers(r.Context(), true); err == nil {
		for _, c := range containers {
			// A volume explorer helper is DocMan looking inside, not a use.
			if isInternalHelper(c) {
				continue
			}
			for _, m := range c.Mounts {
				if m.Type != "volume" || m.Name == "" {
					continue
				}
				usage[m.Name] = append(usage[m.Name], volumeUse{
					Container: c.Name(), Target: m.Destination, ReadOnly: !m.RW, State: c.State,
				})
			}
		}
	}
	out := make([]volumeView, 0, len(volumes))
	for _, v := range volumes {
		uses := usage[v.Name]
		if uses == nil {
			uses = []volumeUse{}
		}
		out = append(out, volumeView{Volume: v, UsedBy: uses})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"volumes": out})
}

func (s *Server) handleInspectVolume(w http.ResponseWriter, r *http.Request) {
	v, err := s.dc.InspectVolume(r.Context(), r.PathValue("name"))
	if err != nil {
		failDocker(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleCreateVolume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string            `json:"name"`
		Driver  string            `json:"driver"`
		Labels  map[string]string `json:"labels"`
		Options map[string]string `json:"options"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if err := validateVolumeName(name); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	driver := strings.TrimSpace(req.Driver)
	if driver == "" {
		driver = "local"
	}
	if drivers, err := s.volumeDrivers(r.Context()); err == nil && !contains(drivers, driver) {
		failHint(w, http.StatusBadRequest, fmt.Sprintf("there is no volume driver %q on this host", driver),
			"available drivers: "+strings.Join(drivers, ", "))
		return
	}
	v, err := s.dc.CreateVolume(r.Context(), name, driver, req.Labels, req.Options)
	if err != nil {
		failDocker(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "volume.create", name, "", true)
	writeJSON(w, http.StatusCreated, v)
}

// volumeDrivers lists the volume drivers the Engine has: always "local", plus
// any volume plugin that is installed and enabled.
func (s *Server) volumeDrivers(ctx context.Context) ([]string, error) {
	info, err := s.dc.Info(ctx)
	if err != nil {
		return nil, err
	}
	drivers := []string{}
	for _, d := range info.Plugins.Volume {
		if d != "" && !contains(drivers, d) {
			drivers = append(drivers, d)
		}
	}
	if !contains(drivers, "local") {
		drivers = append([]string{"local"}, drivers...)
	}
	sort.SliceStable(drivers, func(i, j int) bool { return drivers[i] == "local" && drivers[j] != "local" })
	return drivers, nil
}

func (s *Server) handleVolumeDrivers(w http.ResponseWriter, r *http.Request) {
	drivers, err := s.volumeDrivers(r.Context())
	if err != nil {
		failDocker(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"drivers": drivers})
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func (s *Server) handleRemoveVolume(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	force := boolQuery(r, "force")
	s.closeExplorers(r.Context(), name)
	if err := s.dc.RemoveVolume(r.Context(), name, force); err != nil {
		if dock.Conflict(err) {
			failHint(w, http.StatusConflict, err.Error(),
				"a container is still using this volume; stop and remove it first, or delete with force")
			return
		}
		failDocker(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "volume.remove", name, fmt.Sprintf("force=%v", force), true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePruneVolumes(w http.ResponseWriter, r *http.Request) {
	// Explorer helpers mount volumes, which would keep them from being pruned.
	s.closeExplorers(r.Context(), "")
	report, err := s.dc.PruneVolumes(r.Context())
	if err != nil {
		failDocker(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "volume.prune", "",
		fmt.Sprintf("removed %d volumes", len(report.VolumesDeleted)), true)
	writeJSON(w, http.StatusOK, report)
}

func validateVolumeName(name string) error {
	if name == "" {
		return errors.New("a volume name is required")
	}
	if len(name) > 200 {
		return errors.New("volume name is too long")
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '-', c == '_':
		default:
			return errors.New("volume name may contain only letters, digits, dot, dash and underscore")
		}
	}
	return nil
}

// ---------- networks ----------

func (s *Server) handleListNetworks(w http.ResponseWriter, r *http.Request) {
	networks, err := s.dc.ListNetworks(r.Context())
	if err != nil {
		failDocker(w, err)
		return
	}
	managed := s.nm.NetworkName()
	type view struct {
		*dock.Network
		Managed bool   `json:"managed"`
		Subnet  string `json:"subnet"`
	}
	out := make([]view, 0, len(networks))
	for _, n := range networks {
		subnet := ""
		if len(n.IPAM.Config) > 0 {
			subnet = n.IPAM.Config[0].Subnet
		}
		out = append(out, view{Network: n, Managed: n.Name == managed, Subnet: subnet})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"networks": out})
}

func (s *Server) handleInspectNetwork(w http.ResponseWriter, r *http.Request) {
	n, err := s.dc.InspectNetwork(r.Context(), r.PathValue("id"))
	if err != nil {
		failDocker(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// ---------- fixed addresses ----------

func (s *Server) handleAddresses(w http.ResponseWriter, r *http.Request) {
	plan, pins, err := s.nm.Status(r.Context())
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"network":     plan,
		"addresses":   pins,
		"provisioned": plan != nil,
	})
}

func (s *Server) handleReconcileAddresses(w http.ResponseWriter, r *http.Request) {
	notes := s.nm.Reconcile(r.Context())
	if notes == nil {
		notes = []string{}
	}
	for _, n := range notes {
		s.st.Audit(identityOf(r).Actor(), "address.reconcile", "", n, true)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "notes": notes})
}
