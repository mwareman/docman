package srv

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"docman/internal/crypt"
)

// Background jobs.
//
// Recreating a container — to upgrade it, or to apply a configuration change
// — can cut off the very connection the browser uses: when the container is a
// reverse proxy or tunnel such as Cloudflare's, stopping it drops the request
// that asked for the change. So that work never runs inside a request. The
// request starts a job and returns at once; the job runs to the end on DocMan
// itself, with its own lifetime, and the UI follows its progress, reconnecting
// if it has to. Whether the browser stays connected makes no difference to the
// outcome.

const jobTimeout = 30 * time.Minute

// Job is one piece of background work, as the UI follows it.
type Job struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"` // recreate or update
	Container string `json:"container"`
	OldID     string `json:"old_id"`
	NewID     string `json:"new_id,omitempty"`
	State     string `json:"state"` // running, done, failed or self
	Step      string `json:"step"`
	Error     string `json:"error,omitempty"`
	Actor     string `json:"actor"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at,omitempty"`
	// Result is what the job produced, such as the images an upload loaded.
	Result any `json:"result,omitempty"`
}

type jobRegistry struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

var backgroundJobs = &jobRegistry{jobs: map[string]*Job{}}

func (r *jobRegistry) get(id string) (Job, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *j, true
}

func (r *jobRegistry) update(id string, fn func(*Job)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j, ok := r.jobs[id]; ok {
		fn(j)
	}
}

func (r *jobRegistry) add(j *Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Keep the most recent jobs only; finished ones older than a day go.
	for id, old := range r.jobs {
		if old.EndedAt > 0 && time.Since(time.Unix(old.EndedAt, 0)) > 24*time.Hour {
			delete(r.jobs, id)
		}
	}
	r.jobs[j.ID] = j
}

func (r *jobRegistry) list() []Job {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].StartedAt > out[k].StartedAt })
	return out
}

// startJob runs work in the background and returns the job at once. work
// reports steps through the context (see progressFrom) and returns the new
// container ID.
func (s *Server) startJob(kind, container, oldID, actor string, work func(ctx context.Context) (string, error)) Job {
	job := &Job{
		ID: crypt.RandHex(8), Kind: kind, Container: container, OldID: oldID,
		State: "running", Step: "Starting", Actor: actor, StartedAt: time.Now().Unix(),
	}
	backgroundJobs.add(job)
	snapshot := *job

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
		defer cancel()
		ctx = context.WithValue(ctx, progressKey, func(step string) {
			backgroundJobs.update(job.ID, func(j *Job) { j.Step = step })
		})
		ctx = context.WithValue(ctx, jobIDKey, job.ID)
		newID, err := work(ctx)
		backgroundJobs.update(job.ID, func(j *Job) {
			j.NewID = newID
			j.EndedAt = time.Now().Unix()
			switch {
			case err == errSelfReplacing:
				// DocMan is about to be replaced by a helper; the UI waits for
				// the new instance and finds the container by name.
				j.State, j.Step = "self", "DocMan is recreating itself"
			case err != nil:
				j.State, j.Error = "failed", err.Error()
			default:
				j.State, j.Step = "done", "Finished"
			}
		})
	}()
	return snapshot
}

type progressCtxKey struct{}
type jobIDCtxKey struct{}

var (
	progressKey = progressCtxKey{}
	jobIDKey    = jobIDCtxKey{}
)

// jobIDFromCtx is the ID of the job a context belongs to, or "".
func jobIDFromCtx(ctx context.Context) string {
	id, _ := ctx.Value(jobIDKey).(string)
	return id
}

// jobActor is who started the job a context belongs to.
func jobActor(ctx context.Context) string {
	if j, ok := backgroundJobs.get(jobIDFromCtx(ctx)); ok {
		return j.Actor
	}
	return "docman"
}

// progressFrom returns the job's step reporter from a context, or a no-op.
func progressFrom(ctx context.Context) func(string) {
	if fn, ok := ctx.Value(progressKey).(func(string)); ok {
		return fn
	}
	return func(string) {}
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	j, ok := backgroundJobs.get(r.PathValue("id"))
	if !ok {
		fail(w, http.StatusNotFound, "no such job; DocMan may have restarted since it began")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": backgroundJobs.list()})
}

// ---------- replaced container IDs ----------

// containerMoves remembers which container replaced which, so a page or link
// still holding an old ID can be sent to the container that took its place.
type moves struct {
	mu sync.Mutex
	to map[string]string
}

var containerMoves = &moves{to: map[string]string{}}

func (m *moves) record(oldID, newID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.to) > 2000 {
		m.to = map[string]string{}
	}
	m.to[oldID] = newID
}

// resolve follows a chain of replacements from ref, which may be a full ID or
// a prefix of one, to the newest container. It returns "" when ref was never
// replaced.
func (m *moves) resolve(ref string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := ""
	if len(ref) >= 12 {
		for old, next := range m.to {
			if old == ref || (len(ref) < len(old) && old[:len(ref)] == ref) {
				cur = next
				break
			}
		}
	}
	for i := 0; cur != "" && i < 50; i++ {
		next, ok := m.to[cur]
		if !ok {
			break
		}
		cur = next
	}
	return cur
}
