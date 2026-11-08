package guardian

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"

	"getarm.org/internal/model"
)

const (
	journalKeep  = 200
	journalLimit = 4 << 20
)

// record is one journal line. Later lines for the same action replace
// earlier ones. Priorities hold what each target's restore must undo.
type record struct {
	Action     model.GuardianAction `json:"action"`
	Priorities map[string]Priority  `json:"priorities,omitempty"`
}

// Journal keeps recent actions in memory and appends each change to a
// bounded JSON-lines file, so reversible actions survive a restart.
type Journal struct {
	mu      sync.Mutex
	path    string
	order   []string
	records map[string]*record
	size    int64
}

func OpenJournal(path string) (*Journal, error) {
	j := &Journal{path: path, records: map[string]*record{}}
	if path == "" {
		return j, nil
	}
	f, err := os.Open(path)
	if err == nil {
		defer f.Close()
		// Read the whole file: unreverted records may be anywhere in it.
		// Compaction keeps it near the retention bound.
		b := bufio.NewReader(io.LimitReader(f, 4*journalLimit))
		for {
			line, err := b.ReadBytes('\n')
			var r record
			if len(line) > 0 && json.Unmarshal(line, &r) == nil && r.Action.ID != "" {
				j.put(&r) // torn or truncated lines are skipped
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return j, j.compact()
}

func (j *Journal) put(r *record) {
	if _, ok := j.records[r.Action.ID]; !ok {
		j.order = append(j.order, r.Action.ID)
	}
	j.records[r.Action.ID] = r
	if len(j.order) <= journalKeep {
		return
	}
	// Trim the oldest records, but keep unreverted reversible actions
	// until undone, wherever they are.
	excess := len(j.order) - journalKeep
	keep := j.order[:0]
	for _, id := range j.order {
		if excess > 0 && !retained(j.records[id].Action) {
			delete(j.records, id)
			excess--
			continue
		}
		keep = append(keep, id)
	}
	j.order = keep
}

// detached copies an action's slices, so callers never share memory with
// stored records.
func detached(a model.GuardianAction) model.GuardianAction {
	a.Targets = append([]model.ProcessIdentity(nil), a.Targets...)
	return a
}

func retained(a model.GuardianAction) bool {
	return a.Reversible && a.RevertedAt == nil && a.Outcome != model.GuardianPlanned
}

// Save stores a new or changed action.
func (j *Journal) Save(a model.GuardianAction, priorities map[string]Priority) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	r := &record{Action: a, Priorities: priorities}
	if old := j.records[a.ID]; old != nil && priorities == nil {
		r.Priorities = old.Priorities
	}
	j.put(r)
	if j.path == "" {
		return nil
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if j.size+int64(len(line)) > journalLimit {
		return j.compact()
	}
	f, err := os.OpenFile(j.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := f.Write(line)
	j.size += int64(n)
	return err
}

// compact rewrites the file with current records through a rename.
func (j *Journal) compact() error {
	if j.path == "" {
		return nil
	}
	var b bytes.Buffer
	for _, id := range j.order {
		line, err := json.Marshal(j.records[id])
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
		return err
	}
	tmp := j.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b.Bytes())
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	j.size = int64(b.Len())
	return os.Rename(tmp, j.path)
}

func (j *Journal) Get(id string) (model.GuardianAction, map[string]Priority, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	r := j.records[id]
	if r == nil {
		return model.GuardianAction{}, nil, false
	}
	priorities := make(map[string]Priority, len(r.Priorities))
	for k, v := range r.Priorities {
		priorities[k] = v
	}
	return detached(r.Action), priorities, true
}

// Recent returns actions newest first.
func (j *Journal) Recent(limit int) []model.GuardianAction {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := []model.GuardianAction{}
	for i := len(j.order) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, detached(j.records[j.order[i]].Action))
	}
	return out
}

// Unreverted returns applied reversible actions that were never undone.
func (j *Journal) Unreverted() []model.GuardianAction {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []model.GuardianAction
	for _, id := range j.order {
		a := j.records[id].Action
		if a.Reversible && a.RevertedAt == nil && (a.Outcome == model.GuardianApplied || a.Outcome == model.GuardianPartial || a.Outcome == model.GuardianPending) {
			out = append(out, a)
		}
	}
	return out
}
