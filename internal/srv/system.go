package srv

import (
	"errors"
	"net/http"
	"runtime"
	"strings"
	"time"

	"docman/internal/netmgr"
)

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	info, err := s.dc.Info(ctx)
	if err != nil {
		failDocker(w, err)
		return
	}
	version, _ := s.dc.Version(ctx)
	plan, _ := s.nm.Plan(ctx)

	writeJSON(w, http.StatusOK, map[string]any{
		"docker":            info,
		"docker_version":    version,
		"endpoint":          s.dc.Endpoint(),
		"managed_network":   plan,
		"metrics_available": s.hm.Available(),
		"docman": map[string]any{
			"version":     s.cfg.Version,
			"go":          runtime.Version(),
			"platform":    runtime.GOOS + "/" + runtime.GOARCH,
			"data_dir":    s.cfg.DataDir,
			"tls":         !s.cfg.DisableTLS,
			"session_ttl": int64(s.cfg.SessionTTL.Seconds()),
		},
	})
}

// handleCounts answers the sidebar's four numbers from the monitor's snapshot.
func (s *Server) handleCounts(w http.ResponseWriter, r *http.Request) {
	snap, err := s.mon.current(r.Context())
	if err != nil {
		failDocker(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap.Counts)
}

// handleDiskUsage reports the monitor's last measurement, which Docker takes
// seconds to produce; the first request after start-up measures it itself.
func (s *Server) handleDiskUsage(w http.ResponseWriter, r *http.Request) {
	if u := s.mon.storageNow(); u != nil {
		writeJSON(w, http.StatusOK, u)
		return
	}
	s.mon.refreshStorage(r.Context())
	if u := s.mon.storageNow(); u != nil {
		writeJSON(w, http.StatusOK, u)
		return
	}
	failDocker(w, errors.New("disk usage is not available"))
}

func (s *Server) handleHostMetricsOnce(w http.ResponseWriter, r *http.Request) {
	if !s.hm.Available() {
		fail(w, http.StatusServiceUnavailable, "host metrics are not readable on this platform")
		return
	}
	// Rates need two readings, so take one, pause briefly, and report the
	// second. The streaming endpoint is the better choice for anything
	// repeated; this is here for one-off checks and scripts.
	s.hm.Sample()
	select {
	case <-r.Context().Done():
		return
	case <-time.After(300 * time.Millisecond):
	}
	writeJSON(w, http.StatusOK, s.hm.Sample())
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := s.st.ListAudit(intQuery(r, "limit", 200))
	if err != nil {
		failStore(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// editableSettings are the only keys the settings endpoint will write, with
// their defaults.
var editableSettings = map[string]string{
	"rp_id":               "",
	"log_tail":            "500",
	"stats_interval_ms":   "2000",
	"theme":               "system",
	"confirm_destructive": "true",
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{}
	for key, def := range editableSettings {
		out[key] = s.st.Setting(key, def)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings":               out,
		"managed_network_name":   s.st.Setting(netmgr.SettingNetworkName, netmgr.DefaultNetworkName),
		"managed_network_subnet": s.st.Setting(netmgr.SettingSubnet, ""),
		"rp_id_effective":        s.cfg.RPID,
	})
}

func (s *Server) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if !decodeBody(w, r, &req) {
		return
	}
	changed := []string{}
	for key, value := range req {
		if _, ok := editableSettings[key]; !ok {
			fail(w, http.StatusBadRequest, "unknown setting "+key)
			return
		}
		value = strings.TrimSpace(value)
		if err := s.st.SetSetting(key, value); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		changed = append(changed, key)
	}
	if len(changed) > 0 {
		s.st.Audit(identityOf(r).Actor(), "settings.update", strings.Join(changed, ","), "", true)
	}
	out := map[string]string{}
	for key, def := range editableSettings {
		out[key] = s.st.Setting(key, def)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": out})
}
