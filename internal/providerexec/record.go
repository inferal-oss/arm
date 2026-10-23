// Bounded worker record schema; no store, authority or external operation.
package providerexec

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

type ID string // <=256 UTF-8 bytes, opaque printable ASCII in production
// All numeric quantities remain bounded canonical decimal strings in storage.
type Uint string              // <=19 ASCII bytes; integer <=9223372036854775807
type Stamp string             // <=40 canonical RFC3339Nano UTC bytes
type Start string             // <=64 bytes, exact platform native start token
type Digest string            // exactly64 lowercase hex bytes
type Phase string             // closed vocabulary; max16 ASCII bytes
type State string             // closed vocabulary; max32 ASCII bytes
type Code string              // closed vocabulary; max64 ASCII bytes
type Message string           // <=512 valid UTF-8 bytes, no NUL
type Selected json.RawMessage // encoded contribution <=16384 bytes

const SelectedCap = 16384
const InitialCap = 65536

type Native struct {
	PID             Uint   `json:"pid"`
	PPID            Uint   `json:"ppid"`
	PGID            Uint   `json:"pgid"`
	UID             Uint   `json:"uid"`
	BootSHA256      Digest `json:"boot_sha256"`
	NamespaceSHA256 Digest `json:"namespace_sha256"`
	Start           Start  `json:"start"`
}

type DurableExit struct {
	Kind   State `json:"kind"`
	Code   Uint  `json:"code"`
	Signal Uint  `json:"signal"`
}
type Failure struct {
	Code    Code    `json:"code"`
	Message Message `json:"message"`
}
type Transition struct {
	Phase Phase `json:"phase"`
	At    Stamp `json:"at"`
}
type Pipes struct {
	Stdin   State `json:"stdin"`
	Stdout  State `json:"stdout"`
	Stderr  State `json:"stderr"`
	Control State `json:"control"`
}
type Terminal struct {
	Phase          Phase        `json:"phase"`
	LastKnownPhase Phase        `json:"last_known_phase"`
	Dispatch       State        `json:"dispatch"`
	Protocol       State        `json:"protocol"`
	ResponseSHA256 *Digest      `json:"response_sha256"`
	StderrSHA256   *Digest      `json:"stderr_sha256"`
	StdoutBytes    Uint         `json:"stdout_bytes"`
	StderrBytes    Uint         `json:"stderr_bytes"`
	ProviderExit   *DurableExit `json:"provider_exit"`
	HelperExit     *DurableExit `json:"helper_exit"`
	Parent         *Native      `json:"parent"`
	Helper         *Native      `json:"helper"`
	Provider       *Native      `json:"provider"`
	Group          State        `json:"group"`
	Pipes          Pipes        `json:"pipes"`
	MonitorClosed  bool         `json:"monitor_closed"`
	StartedAt      *Stamp       `json:"started_at"`
	ObservedAt     Stamp        `json:"observed_at"`
	WorkerLease    ID           `json:"worker_lease"`
	BudgetState    State        `json:"budget_state"`
	Failure        *Failure     `json:"failure"`
	Result         Selected     `json:"selected_result"`
	// Initial frame records remain immutable. Only fixed phase slots are appended:
	// prepared/start/hello/dispatch/observed/first-unknown/native-closure/settled.
	// Slots are named, write-once facts; positive recovery shares native-closure.
	Transitions [8]Transition `json:"transitions"`
}

type Initial struct {
	Attempt         ID              `json:"attempt"`
	Generation      ID              `json:"generation"`
	Authority       ID              `json:"authority"`
	Request         json.RawMessage `json:"request"`
	RequestSHA256   Digest          `json:"request_sha256"`
	WorkerLease     ID              `json:"worker_lease"`
	TempReservation ID              `json:"temp_reservation"`
	CPU             Uint            `json:"cpu_millicores"`
	Memory          Uint            `json:"memory_bytes"`
	TempDisk        Uint            `json:"temp_bytes"`
}

func maxString(t reflect.Type) int {
	switch t {
	case reflect.TypeFor[ID]():
		return 256
	case reflect.TypeFor[Uint]():
		return 19
	case reflect.TypeFor[Stamp]():
		return 40
	case reflect.TypeFor[Digest]():
		return 64
	case reflect.TypeFor[Start]():
		return 64
	case reflect.TypeFor[Phase]():
		return 16
	case reflect.TypeFor[State]():
		return 32
	case reflect.TypeFor[Code]():
		return 64
	case reflect.TypeFor[Message]():
		return 512
	}
	panic("unbounded string type: " + t.String())
}

// Size proof: json.Marshal emits at most six bytes per valid UTF-8 input byte
// (including HTML/control/U+2028 escaping), plus two quotes. This deliberately
// overcounts decimal/digest/enum ASCII values and all non-ASCII input.
func maxSize(t reflect.Type) int {
	if t == reflect.TypeFor[Selected]() {
		return SelectedCap
	}
	switch t.Kind() {
	case reflect.Pointer:
		n := maxSize(t.Elem())
		if n < 4 {
			return 4
		}
		return n
	case reflect.Bool:
		return 5
	case reflect.String:
		return 2 + 6*maxString(t)
	case reflect.Array:
		return 2 + (t.Len() - 1) + t.Len()*maxSize(t.Elem())
	case reflect.Struct:
		n := 2
		for i := 0; i < t.NumField(); i++ {
			if i > 0 {
				n++
			}
			k, _ := json.Marshal(t.Field(i).Tag.Get("json"))
			n += len(k) + 1 + maxSize(t.Field(i).Type)
		}
		return n
	default:
		panic("unbounded terminal type: " + t.String())
	}
}

// Selected.MarshalJSON and enclosing serialization use the same standard encoder;
// cap the final escaped contribution, not raw canonical Data or character count.
func (s Selected) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(json.RawMessage(s))
	if err != nil {
		return nil, err
	}
	if len(b) > SelectedCap {
		return nil, fmt.Errorf("selected result exceeds escaped-byte cap")
	}
	return b, nil
}
func (s *Selected) UnmarshalJSON(b []byte) error {
	var raw json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	bounded := Selected(append([]byte(nil), raw...))
	if _, err := bounded.MarshalJSON(); err != nil {
		return err
	}
	*s = bounded
	return nil
}

func check(v reflect.Value) error {
	if v.Type() == reflect.TypeFor[Selected]() {
		_, err := v.Interface().(Selected).MarshalJSON()
		return err
	}
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			return check(v.Elem())
		}
	case reflect.String:
		if !utf8.ValidString(v.String()) || strings.ContainsRune(v.String(), 0) || len(v.String()) > maxString(v.Type()) {
			return fmt.Errorf("string bound %s", v.Type())
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := check(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if err := check(v.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Bool:
	default:
		return fmt.Errorf("unbounded terminal field %s", v.Type())
	}
	return nil
}
func encodeTerminal(v Terminal) ([]byte, error) {
	if err := check(reflect.ValueOf(v)); err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) > maxSize(reflect.TypeFor[Terminal]()) {
		return nil, fmt.Errorf("derived proof violated")
	}
	return b, nil
}
func encodeInitial(v Initial) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) > InitialCap {
		return nil, fmt.Errorf("escaped initial bytes exceed cap")
	}
	return b, nil
}
