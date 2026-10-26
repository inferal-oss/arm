package pressure

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"
)

// State has a deterministic JSON representation so an existing transaction
// journal can store it beside the emitted actions. No native authority is
// restored by decoding state; the epoch and provider fence still apply.
type stateWire struct {
	Epoch           string               `json:"epoch"`
	LastTick        time.Duration        `json:"last_tick"`
	LastNow         time.Time            `json:"last_now"`
	Resources       map[Resource]Episode `json:"resources"`
	Pauses          []PauseRecord        `json:"pauses"`
	JobWarnings     []warningWire        `json:"job_warnings"`
	Unresolved      []UnresolvedControl  `json:"unresolved"`
	ControlRevision uint64               `json:"control_revision"`
}

type stateRawWire struct {
	Epoch           string          `json:"epoch"`
	LastTick        time.Duration   `json:"last_tick"`
	LastNow         time.Time       `json:"last_now"`
	Resources       json.RawMessage `json:"resources"`
	Pauses          json.RawMessage `json:"pauses"`
	JobWarnings     json.RawMessage `json:"job_warnings"`
	Unresolved      json.RawMessage `json:"unresolved"`
	ControlRevision uint64          `json:"control_revision"`
}

type warningWire struct {
	Resource Resource   `json:"resource"`
	Job      JobID      `json:"job"`
	Warning  JobWarning `json:"warning"`
}

func (s State) MarshalJSON() ([]byte, error) {
	if err := validateStoredState(s, maxJournalJobs); err != nil {
		return nil, err
	}
	w := stateWire{Epoch: s.Epoch, LastTick: s.LastTick, LastNow: s.LastNow,
		Resources: s.Resources, ControlRevision: s.ControlRevision}
	for jid, record := range s.Pauses {
		if record.Job != jid {
			return nil, errors.New("pressure: pause map identity mismatch")
		}
		w.Pauses = append(w.Pauses, record)
	}
	slices.SortFunc(w.Pauses, func(a, b PauseRecord) int { return compareJob(a.Job, b.Job) })
	for resource, roster := range s.JobWarnings {
		for jid, warning := range roster {
			w.JobWarnings = append(w.JobWarnings, warningWire{Resource: resource, Job: jid, Warning: warning})
		}
	}
	slices.SortFunc(w.JobWarnings, func(a, b warningWire) int {
		if a.Resource < b.Resource {
			return -1
		}
		if a.Resource > b.Resource {
			return 1
		}
		return compareJob(a.Job, b.Job)
	})
	for _, obligation := range s.Unresolved {
		w.Unresolved = append(w.Unresolved, obligation)
	}
	slices.SortFunc(w.Unresolved, func(a, b UnresolvedControl) int {
		if a.Key < b.Key {
			return -1
		}
		if a.Key > b.Key {
			return 1
		}
		return 0
	})
	encoded, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxJournalBytes {
		return nil, errors.New("pressure: journal exceeds byte bound")
	}
	return encoded, nil
}

func (s *State) UnmarshalJSON(data []byte) error {
	if len(data) > maxJournalBytes || !utf8.Valid(data) || !validJSONUnicodeEscapes(data) {
		return errors.New("pressure: invalid or oversized journal encoding")
	}
	var w stateRawWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	resources, err := decodeResources(w.Resources)
	if err != nil {
		return err
	}
	pauses, err := decodeBoundedSlice[PauseRecord](w.Pauses, maxJournalJobs)
	if err != nil {
		return err
	}
	warnings, err := decodeBoundedSlice[warningWire](w.JobWarnings, 3*maxJournalJobs)
	if err != nil {
		return err
	}
	unresolved, err := decodeBoundedSlice[UnresolvedControl](w.Unresolved, maxJournalJobs)
	if err != nil {
		return err
	}
	next := State{Epoch: w.Epoch, LastTick: w.LastTick, LastNow: w.LastNow,
		Resources: resources, Pauses: make(map[JobID]PauseRecord, len(pauses)),
		JobWarnings: make(map[Resource]map[JobID]JobWarning), ControlRevision: w.ControlRevision,
		Unresolved: make(map[string]UnresolvedControl, len(unresolved))}
	for _, record := range pauses {
		if _, exists := next.Pauses[record.Job]; exists {
			return errors.New("pressure: duplicate pause in journal")
		}
		next.Pauses[record.Job] = record
	}
	for _, entry := range warnings {
		if next.JobWarnings[entry.Resource] == nil {
			next.JobWarnings[entry.Resource] = make(map[JobID]JobWarning)
		}
		if _, exists := next.JobWarnings[entry.Resource][entry.Job]; exists {
			return errors.New("pressure: duplicate warning in journal")
		}
		next.JobWarnings[entry.Resource][entry.Job] = entry.Warning
	}
	for _, obligation := range unresolved {
		if _, exists := next.Unresolved[obligation.Key]; exists {
			return errors.New("pressure: duplicate unresolved control in journal")
		}
		next.Unresolved[obligation.Key] = obligation
	}
	if err := validateStoredState(next, maxJournalJobs); err != nil {
		return err
	}
	*s = next
	return nil
}

func decodeBoundedSlice[T any](raw json.RawMessage, max int) ([]T, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('[') {
		return nil, errors.New("pressure: invalid journal array")
	}
	var out []T
	for decoder.More() {
		if len(out) >= max {
			return nil, errors.New("pressure: journal array exceeds bound")
		}
		var value T
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim(']') {
		return nil, errors.New("pressure: invalid journal array end")
	}
	return out, nil
}

func decodeResources(raw json.RawMessage) (map[Resource]Episode, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errors.New("pressure: invalid resource map")
	}
	out := make(map[Resource]Episode)
	for decoder.More() {
		if len(out) >= 3 {
			return nil, errors.New("pressure: resource map exceeds bound")
		}
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		resource, ok := key.(string)
		if !ok || !validResource(Resource(resource)) {
			return nil, errors.New("pressure: invalid resource key")
		}
		if _, exists := out[Resource(resource)]; exists {
			return nil, errors.New("pressure: duplicate resource key")
		}
		var episode Episode
		if err := decoder.Decode(&episode); err != nil {
			return nil, err
		}
		out[Resource(resource)] = episode
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return nil, errors.New("pressure: invalid resource map end")
	}
	return out, nil
}

// encoding/json substitutes U+FFFD for isolated surrogate escapes. Reject
// them before decoding so persisted identity bytes cannot silently change.
func validJSONUnicodeEscapes(data []byte) bool {
	inString := false
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '"':
			inString = !inString
		case '\\':
			if !inString {
				continue
			}
			i++
			if i >= len(data) {
				return false
			}
			if data[i] != 'u' {
				continue
			}
			if i+4 >= len(data) {
				return false
			}
			first, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
			if err != nil {
				return false
			}
			i += 4
			if first >= 0xdc00 && first <= 0xdfff {
				return false
			}
			if first >= 0xd800 && first <= 0xdbff {
				if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
					return false
				}
				second, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
				if err != nil || second < 0xdc00 || second > 0xdfff {
					return false
				}
				i += 6
			}
		}
	}
	return !inString
}
