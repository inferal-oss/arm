package manager

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"
	"unicode/utf8"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
)

const daemonConfigEvent = "daemon.configuration_applied"

// This canonical last snapshot must survive any future event retention. Older
// daemon-launch.json files cannot establish a journaled previous value.
type daemonConfigTransition struct {
	Schema    int                    `json:"schema"`
	Revision  int64                  `json:"revision"`
	Host      string                 `json:"host"`
	Actor     string                 `json:"actor"`
	At        time.Time              `json:"at"`
	Operation string                 `json:"operation"`
	Previous  *daemonconfig.Settings `json:"previous"`
	Settings  daemonconfig.Settings  `json:"settings"`
}

// OpenConfigured returns no manager, recovery or admission authority before the
// exact configured capacity has a confirmed durable startup decision.
func OpenConfigured(home string, settings daemonconfig.Settings) (*Manager, error) {
	if err := settings.Validate(); err != nil {
		return nil, model.Err("invalid_config", err.Error())
	}
	return openWithStartup(home, settings.Resources, leaseclock.Read, &settings)
}

func configObject(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("configuration evidence must be an object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok || fields[key] != nil {
			return nil, fmt.Errorf("duplicate configuration evidence field")
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return nil, err
		}
		fields[key] = value
	}
	if _, err = d.Token(); err != nil {
		return nil, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("trailing configuration evidence")
	}
	if len(fields) != len(keys) {
		return nil, fmt.Errorf("configuration evidence fields differ from schema")
	}
	for _, key := range keys {
		if fields[key] == nil || bytes.Equal(fields[key], []byte("null")) && key != "previous" {
			return nil, fmt.Errorf("configuration evidence lacks %s", key)
		}
	}
	return fields, nil
}

func configSettingsShape(raw []byte) error {
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		return err
	}
	keys := []string{"resources", "listen"}
	if _, ok := shape["storage_pools"]; ok {
		keys = append(keys, "storage_pools")
	}
	fields, err := configObject(raw, keys...)
	if err != nil {
		return err
	}
	_, err = configObject(fields["resources"], "cpu", "memory_bytes", "disk_bytes")
	return err
}

func decodeDaemonConfig(raw []byte, into *daemonConfigTransition) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("configuration event is not valid UTF-8")
	}
	fields, err := configObject(raw, "schema", "revision", "host", "actor", "at", "operation", "previous", "settings")
	if err != nil {
		return err
	}
	if err = configSettingsShape(fields["settings"]); err != nil {
		return err
	}
	if !bytes.Equal(fields["previous"], []byte("null")) {
		if err = configSettingsShape(fields["previous"]); err != nil {
			return err
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(into)
}

// Caller owns startup exclusion. The existing events table is sufficient: no
// metadata, format migration or owner/nested policy-budget change is introduced.
func (m *Manager) applyStartupConfiguration(settings daemonconfig.Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var raw []byte
	var entity, stamp string
	var size, sequence int64
	var textFields bool
	err = tx.QueryRow(`SELECT sequence,
		CASE WHEN typeof(entity)='text' AND length(CAST(entity AS BLOB))<=256 THEN entity ELSE '' END,
		CASE WHEN typeof(at)='text' AND length(CAST(at AS BLOB))<=64 THEN at ELSE '' END,
		typeof(type)='text' AND typeof(data)='text' AND typeof(entity)='text' AND typeof(at)='text',
		length(CAST(data AS BLOB)),
		CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB))<=32768 THEN data ELSE '' END
		FROM events WHERE CAST(type AS BLOB)=CAST(? AS BLOB) ORDER BY sequence DESC LIMIT 1`, daemonConfigEvent).Scan(&sequence, &entity, &stamp, &textFields, &size, &raw)
	var previous *daemonconfig.Settings
	revision := int64(1)
	operation := "initialize-observed-startup"
	if err == nil {
		var old daemonConfigTransition
		envelopeAt, parseErr := time.Parse(time.RFC3339Nano, stamp)
		if !textFields || sequence < 1 || parseErr != nil || envelopeAt.IsZero() {
			return model.Err("invalid_config_journal", "last configuration event has an invalid SQL sequence/type/timestamp envelope")
		}
		if size <= 0 || size > 32768 {
			return model.Err("invalid_config_journal", "last configuration event exceeds its 32 KiB bound")
		}
		if err = decodeDaemonConfig(raw, &old); err != nil {
			return model.Err("invalid_config_journal", "last configuration event is malformed; inspect retained evidence before restart: "+err.Error())
		}
		if entity != m.host || old.Schema != 1 || old.Host != m.host || old.Actor != m.owner || !envelopeAt.Equal(old.At) || old.Revision < 1 || old.Revision == 1<<63-1 || old.Settings.Validate() != nil || old.Revision == 1 && (old.Previous != nil || old.Operation != "initialize-observed-startup") || old.Revision > 1 && (old.Previous == nil || old.Operation != "replace-startup") || old.Previous != nil && old.Previous.Validate() != nil {
			return model.Err("invalid_config_journal", "last configuration event identity, revision or snapshot is invalid; no fallback baseline is assumed")
		}
		previous = &old.Settings
		revision = old.Revision + 1
		operation = "replace-startup"
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previous == nil || !reflect.DeepEqual(*previous, settings) {
		change := daemonConfigTransition{Schema: 1, Revision: revision, Host: m.host, Actor: m.owner, At: time.Now().UTC(), Operation: operation, Previous: previous, Settings: settings}
		data, err := json.Marshal(change)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO events(type,entity,at,data) VALUES(?,?,?,?)`, daemonConfigEvent, m.host, change.At.Format(time.RFC3339Nano), string(data)); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("configuration transition was not confirmed durable; backend will not serve: %w", err)
	}
	m.capacity = settings.Resources
	return nil
}
