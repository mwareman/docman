package srv

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"docman/internal/crypt"
	"docman/internal/dock"
)

// SelfReplaceCommand is the argument that makes the docman binary run as the
// short-lived helper that recreates DocMan's own container.
const SelfReplaceCommand = "replace-self"

// SelfReplacePlanEnv carries the base64 JSON ReplacePlan into the helper.
const SelfReplacePlanEnv = "DOCMAN_REPLACE_PLAN"

// handOffSelfReplace starts a helper container from DocMan's own image that
// runs plan once DocMan has answered the request. The helper reaches the Engine
// the same way DocMan does: the same socket mount, or the same network for a
// TCP endpoint.
func (s *Server) handOffSelfReplace(ctx context.Context, inspect map[string]any, plan *dock.ReplacePlan) error {
	cfg, _ := inspect["Config"].(map[string]any)
	host, _ := inspect["HostConfig"].(map[string]any)
	image, _ := inspect["Image"].(string)
	if image == "" {
		return errors.New("could not read the image DocMan is running from")
	}

	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}

	helperHost := map[string]any{
		"NetworkMode": host["NetworkMode"],
		"SecurityOpt": []string{"no-new-privileges:true"},
		// Never restart: a second run would find nothing to replace.
		"RestartPolicy": map[string]any{"Name": "no"},
		// DocMan's image declares /data a volume; a tmpfs there stops Docker
		// creating an anonymous volume for the helper.
		"Tmpfs": map[string]string{"/data": ""},
	}
	if groups, ok := host["GroupAdd"]; ok && groups != nil {
		helperHost["GroupAdd"] = groups
	}
	if sock := s.dc.SocketPath(); sock != "" {
		bind := socketBind(inspect, sock)
		if bind == "" {
			return fmt.Errorf("could not tell how %s is mounted into DocMan, so a helper cannot reach Docker; "+
				"recreate DocMan from the host instead (docker compose up -d --force-recreate)", sock)
		}
		helperHost["Binds"] = []string{bind}
	}

	entrypoint := []string{"/docman"}
	if ep, ok := cfg["Entrypoint"].([]any); ok && len(ep) > 0 {
		if first, ok := ep[0].(string); ok && first != "" {
			entrypoint = []string{first}
		}
	}

	body := map[string]any{
		"Image":      image,
		"Entrypoint": entrypoint,
		"Cmd":        []string{SelfReplaceCommand},
		"Env": []string{
			"DOCMAN_DOCKER_HOST=" + s.cfg.DockerHost,
			SelfReplacePlanEnv + "=" + base64.StdEncoding.EncodeToString(raw),
		},
		"User":       cfg["User"],
		"Labels":     map[string]string{"docman.helper": SelfReplaceCommand},
		"HostConfig": helperHost,
	}

	name := trimName(plan.OldName) + ".docman-replace-" + crypt.RandHex(3)
	created, err := s.dc.CreateContainer(ctx, name, body)
	if err != nil {
		return fmt.Errorf("could not create the helper that recreates DocMan: %w", err)
	}
	if err := s.dc.StartContainer(ctx, created.ID); err != nil {
		_ = s.dc.RemoveContainer(context.Background(), created.ID, true, true)
		return fmt.Errorf("could not start the helper that recreates DocMan: %w", err)
	}
	s.logf("recreate: helper %s is recreating DocMan; this instance will now stop", name)
	return nil
}

// socketBind turns the mount that exposes sock inside DocMan into a bind for
// the helper: the socket itself, or a directory above it.
func socketBind(inspect map[string]any, sock string) string {
	mounts, _ := inspect["Mounts"].([]any)
	for _, m := range mounts {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		src, _ := mm["Source"].(string)
		dst, _ := mm["Destination"].(string)
		if src == "" || dst == "" {
			continue
		}
		if dst == sock || strings.HasPrefix(sock, strings.TrimSuffix(dst, "/")+"/") {
			return src + ":" + dst
		}
	}
	return ""
}

// RunSelfReplace is the helper's whole life: wait for the DocMan that started
// it to finish answering, then swap its container for the rebuilt one. On
// success the helper removes itself. On failure the original DocMan is rolled
// back and restarted, and SweepSelfReplaceHelpers removes the helper then.
func RunSelfReplace(ctx context.Context, dc *dock.Client, logf func(string, ...any)) error {
	raw, err := base64.StdEncoding.DecodeString(os.Getenv(SelfReplacePlanEnv))
	if err != nil {
		return fmt.Errorf("unreadable %s: %w", SelfReplacePlanEnv, err)
	}
	var plan dock.ReplacePlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return fmt.Errorf("unreadable %s: %w", SelfReplacePlanEnv, err)
	}
	if plan.OldID == "" || plan.NewName == "" || plan.Body == nil {
		return errors.New("the replace plan is incomplete")
	}

	// Give DocMan time to send its 202 before it is stopped.
	time.Sleep(3 * time.Second)

	logf("recreating %s", plan.OldName)
	id, err := dock.Replace(ctx, dc, &plan, logf)
	if err != nil {
		return err
	}
	logf("%s recreated as %s", plan.NewName, id)

	if self, err := os.Hostname(); err == nil && self != "" {
		_ = dc.RemoveContainer(context.Background(), self, true, true)
	}
	return nil
}

// SweepSelfReplaceHelpers removes any finished recreate helper, copying its
// log into DocMan's first so a failed self-recreate can still be diagnosed.
// It runs at startup, which covers a helper that failed (DocMan was rolled
// back) and one that succeeded but could not remove itself.
func (s *Server) SweepSelfReplaceHelpers(ctx context.Context) {
	// After a rollback DocMan can be back before its helper has exited, so a
	// running helper is waited for rather than skipped.
	for attempt := 0; attempt < 20; attempt++ {
		if !s.sweepHelpersOnce(ctx) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// sweepHelpersOnce removes the finished helpers and reports whether any are
// still running.
func (s *Server) sweepHelpersOnce(ctx context.Context) bool {
	list, err := s.dc.ListContainers(ctx, true)
	if err != nil {
		s.logf("recreate helpers: could not list containers: %v", err)
		return false
	}
	pending := false
	for _, c := range list {
		if c.Labels["docman.helper"] != SelfReplaceCommand {
			continue
		}
		if c.State == "running" {
			pending = true
			continue
		}
		name := c.Name()
		if rc, err := s.dc.ContainerLogs(ctx, c.ID, dock.LogOptions{Stdout: true, Stderr: true, Tail: "200"}); err == nil {
			for {
				frame, err := dock.ReadFrame(rc)
				if err != nil {
					break
				}
				for _, line := range strings.Split(strings.TrimRight(string(frame.Data), "\n"), "\n") {
					s.logf("recreate helper %s: %s", name, line)
				}
			}
			rc.Close()
		}
		if err := s.dc.RemoveContainer(ctx, c.ID, true, true); err != nil {
			s.logf("recreate helpers: could not remove %s: %v", name, err)
		} else {
			s.logf("recreate helpers: removed %s", name)
		}
	}
	return pending
}
