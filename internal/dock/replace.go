package dock

import (
	"context"
	"fmt"
)

// ReplacePlan is everything needed to swap a container for a rebuilt copy of
// itself. It is plain data so it can be handed to a helper container when the
// container being replaced is the one doing the replacing.
type ReplacePlan struct {
	OldID      string                      `json:"old_id"`
	OldName    string                      `json:"old_name"`
	NewName    string                      `json:"new_name"`
	Parked     string                      `json:"parked"`
	Body       map[string]any              `json:"body"`
	Extras     map[string]EndpointSettings `json:"extras"`
	WasRunning bool                        `json:"was_running"`
	Start      bool                        `json:"start"`
	// WasPaused puts the replacement back into the paused state it replaces.
	WasPaused bool `json:"was_paused,omitempty"`
	// Progress, when set, is told each step as it begins.
	Progress func(step string) `json:"-"`
}

func (p *ReplacePlan) step(s string) {
	if p.Progress != nil {
		p.Progress(s)
	}
}

// Replace stops the old container, sets it aside under p.Parked, creates the
// replacement, reattaches its extra networks, deletes the original and starts
// the replacement. If the replacement cannot be created the original is
// renamed back and restarted, so a failed edit is not a lost container.
func Replace(ctx context.Context, dc *Client, p *ReplacePlan, logf func(string, ...any)) (string, error) {
	if p.WasRunning {
		p.step("Stopping " + p.OldName)
		if p.WasPaused {
			// A paused container cannot be stopped until it is resumed.
			_ = dc.UnpauseContainer(ctx, p.OldID)
		}
		if err := dc.StopContainer(ctx, p.OldID, 10); err != nil && !Conflict(err) {
			return "", fmt.Errorf("could not stop %s: %w", p.OldName, err)
		}
	}

	p.step("Creating the replacement")
	if err := dc.RenameContainer(ctx, p.OldID, p.Parked); err != nil {
		return "", fmt.Errorf("could not set the existing container aside: %w", err)
	}
	restore := func() {
		// Use a background context: the request may already be cancelled.
		bg := context.Background()
		if err := dc.RenameContainer(bg, p.OldID, p.OldName); err != nil {
			logf("recreate rollback: could not restore the name %q: %v", p.OldName, err)
		}
		if p.WasRunning {
			if err := dc.StartContainer(bg, p.OldID); err != nil {
				logf("recreate rollback: could not restart %q: %v", p.OldName, err)
			}
		}
	}

	created, err := dc.CreateContainer(ctx, p.NewName, p.Body)
	if err != nil {
		restore()
		return "", fmt.Errorf("could not create the updated container: %w", err)
	}

	for name, ep := range p.Extras {
		cfg := &EndpointSettings{Aliases: ep.Aliases}
		if ep.IPAMConfig != nil && ep.IPAMConfig.IPv4Address != "" {
			cfg.IPAMConfig = &EndpointIPAM{IPv4Address: ep.IPAMConfig.IPv4Address}
		}
		if err := dc.ConnectNetwork(ctx, name, created.ID, cfg); err != nil {
			logf("recreate: could not reattach %q to %q: %v", p.NewName, name, err)
		}
	}

	if err := dc.RemoveContainer(ctx, p.OldID, true, false); err != nil {
		logf("recreate: the replacement is in place but the old container %q could not be removed: %v", p.Parked, err)
	}

	if p.Start {
		p.step("Starting " + p.NewName)
		if err := dc.StartContainer(ctx, created.ID); err != nil {
			return created.ID, fmt.Errorf("the container was updated but would not start: %w", err)
		}
		if p.WasPaused {
			if err := dc.PauseContainer(ctx, created.ID); err != nil {
				logf("recreate: %s started but could not be paused again: %v", p.NewName, err)
			}
		}
	}
	return created.ID, nil
}
