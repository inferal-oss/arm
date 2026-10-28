package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"getarm.org/internal/model"
	"io"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// C2 only: closed ordinary wire inspection, not a new model or format parser.
// All current fields must match their exact declared spelling; future fields
// require source review. Collection bounds are independent of total raw bytes.
func providerPairC2Fields(t reflect.Type, fields map[string]reflect.StructField) error {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			if e := providerPairC2Fields(f.Type, fields); e != nil {
				return e
			}
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if _, ok := fields[name]; ok {
			return fmt.Errorf("ambiguous ordinary schema")
		}
		fields[name] = f
	}
	return nil
}
func providerPairC2Shape(raw []byte, t reflect.Type, path string) error {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		// Ordinary model pointers include legitimate unknown allocation/identity
		// observations. They do not erase a required scalar or raw LeaseRef field.
		if t.Kind() == reflect.Pointer {
			return nil
		}
		return fmt.Errorf("null ordinary field at %s", path)
	}
	if t.Kind() == reflect.Pointer {
		return providerPairC2Shape(raw, t.Elem(), path)
	}
	if t == reflect.TypeOf(time.Time{}) {
		var v string
		if json.Unmarshal(raw, &v) != nil || !providerPairStamp(v) {
			return fmt.Errorf("ordinary timestamp")
		}
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return fmt.Errorf("ordinary object required")
		}
		fields := map[string]reflect.StructField{}
		// These accounting-bearing model boundaries are explicitly closed rather
		// than automatically accepting future optional model fields.
		allowed := ""
		switch t {
		case reflect.TypeOf(model.Lease{}):
			allowed = "elapsed_deadline cargo pool_binding pool_error budget_binding budget_policy budget_error queue_info id host owner owner_username request state outcome recovery_reason failure created_at started_at expires_at ended_at revision pid managed_dir exit_code capabilities artifact ports process_identity usage workload"
		case reflect.TypeOf(model.Request{}):
			allowed = "forward_source budget start_deadline_at origin_certificate origin_grant_revision origin_key origin_peer host reason project agent resources ttl_ns idempotency_key queue profile kind storage_lease service_lease ports port_lease pools usage_policy"
		case reflect.TypeOf(RunRequest{}):
			allowed = "forward_source budget start_deadline_at origin_certificate origin_grant_revision origin_key origin_peer host reason project agent resources ttl_ns idempotency_key queue profile kind storage_lease service_lease ports port_lease pools usage_policy command cwd ssh"
		case reflect.TypeOf(model.Resources{}):
			allowed = "cpu memory_bytes disk_bytes"
		}
		if allowed != "" {
			for key := range object {
				if !strings.Contains(" "+allowed+" ", " "+key+" ") {
					return fmt.Errorf("unreviewed ordinary accounting field")
				}
			}
		}
		if e := providerPairC2Fields(t, fields); e != nil {
			return e
		}
		for key := range object {
			if _, ok := fields[key]; !ok {
				return fmt.Errorf("unsupported ordinary field at %s", path)
			}
		}
		for key, f := range fields {
			v, ok := object[key]
			if !ok {
				if strings.Contains(f.Tag.Get("json"), ",omitempty") {
					continue
				}
				return fmt.Errorf("missing ordinary field at %s.%s", path, key)
			}
			if e := providerPairC2Shape(v, f.Type, path+"."+key); e != nil {
				return e
			}
		}
	case reflect.Slice, reflect.Array:
		limit := 64
		if path == "$.capabilities" {
			limit = 128
		}
		if strings.HasSuffix(path, ".path") {
			limit = 16
		}
		if t.Kind() == reflect.Array {
			limit = t.Len()
		}
		values, e := providerPairArray(raw, limit, t.Kind() == reflect.Array)
		if e != nil {
			return e
		}
		for _, v := range values {
			if e = providerPairC2Shape(v, t.Elem(), path+"[]"); e != nil {
				return e
			}
		}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return fmt.Errorf("unsupported ordinary map")
		}
		var values map[string]json.RawMessage
		if json.Unmarshal(raw, &values) != nil || values == nil || len(values) > 64 {
			return fmt.Errorf("ordinary map bound")
		}
		for key, v := range values {
			if len(key) == 0 || len(key) > 128 {
				return fmt.Errorf("ordinary map key bound")
			}
			if e := providerPairC2Shape(v, t.Elem(), path+"{}"); e != nil {
				return e
			}
		}
	case reflect.Interface:
		return fmt.Errorf("unreviewed ordinary interface")
	}
	return nil
}
func decodeProviderPairC2JSON(raw []byte, out any, limit int) error {
	if len(raw) == 0 || len(raw) > limit || !utf8.Valid(raw) {
		return fmt.Errorf("ordinary JSON byte/UTF8 bound")
	}
	if e := providerPairUnicode(raw); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := providerPairTokens(d, raw, 0, 24); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("ordinary trailing JSON")
	}
	t := reflect.TypeOf(out)
	if t == nil || t.Kind() != reflect.Pointer || reflect.ValueOf(out).IsNil() {
		return fmt.Errorf("ordinary decode target")
	}
	if e := providerPairC2Shape(raw, t.Elem(), "$"); e != nil {
		return e
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

// Bounded exact decimal value shared by promise rounding and saved-run binding.
// No float64 or ceil-to-millis equality can certify raw Request consistency.
func providerPairC2Decimal(raw []byte) (*big.Rat, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 80 {
		return nil, fmt.Errorf("CPU scalar bound")
	}
	s := string(raw)
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exponent, e := strconv.ParseInt(s[i+1:], 10, 16)
		if e != nil || exponent < -400 || exponent > 400 {
			return nil, fmt.Errorf("CPU exponent bound")
		}
	}
	var n json.Number
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return nil, fmt.Errorf("CPU number required")
	}
	if _, e := d.Token(); e != io.EOF {
		return nil, fmt.Errorf("trailing CPU scalar")
	}
	var ok bool
	n, ok = v.(json.Number)
	if !ok {
		return nil, fmt.Errorf("CPU number required")
	}
	q, ok := new(big.Rat).SetString(string(n))
	if !ok || q.Sign() < 0 {
		return nil, fmt.Errorf("CPU nonnegative decimal required")
	}
	return q, nil
}

// Every positive exact promise rounds UP, including decimal underflow.
func providerPairC2CPU(raw []byte) (int64, error) {
	q, e := providerPairC2Decimal(raw)
	if e != nil {
		return 0, e
	}
	q.Mul(q, big.NewRat(1000, 1))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(q.Num(), q.Denom(), remainder)
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("CPU millis overflow")
	}
	return quotient.Int64(), nil
}
func providerPairC2QuantityFromRaw(raw []byte) (providerPairQuantity, error) {
	var top, request, resources map[string]json.RawMessage
	if json.Unmarshal(raw, &top) != nil || json.Unmarshal(top["request"], &request) != nil || json.Unmarshal(request["resources"], &resources) != nil {
		return providerPairQuantity{}, fmt.Errorf("ordinary resource object")
	}
	var q providerPairQuantity
	var e error
	q.cpuMillis, e = providerPairC2CPU(resources["cpu"])
	if e != nil {
		return q, e
	}
	if json.Unmarshal(resources["memory_bytes"], &q.memoryBytes) != nil || json.Unmarshal(resources["disk_bytes"], &q.diskBytes) != nil || q.memoryBytes < 0 || q.diskBytes < 0 {
		return providerPairQuantity{}, fmt.Errorf("ordinary resource bytes")
	}
	return q, nil
}

func providerPairC2RequestCPU(raw []byte, lease bool) ([]byte, error) {
	var fields, resources map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, fmt.Errorf("raw Request object required")
	}
	if lease {
		var request map[string]json.RawMessage
		if json.Unmarshal(fields["request"], &request) != nil {
			return nil, fmt.Errorf("raw lease Request required")
		}
		fields = request
	}
	if json.Unmarshal(fields["resources"], &resources) != nil || resources["cpu"] == nil {
		return nil, fmt.Errorf("raw CPU promise required")
	}
	return resources["cpu"], nil
}
func providerPairC2SameRequestCPU(lease, run []byte) error {
	a, e := providerPairC2RequestCPU(lease, true)
	if e != nil {
		return e
	}
	b, e := providerPairC2RequestCPU(run, false)
	if e != nil {
		return e
	}
	x, e := providerPairC2Decimal(a)
	if e != nil {
		return e
	}
	y, e := providerPairC2Decimal(b)
	if e != nil {
		return e
	}
	if x.Cmp(y) != 0 {
		return fmt.Errorf("exact saved-run CPU promise differs")
	}
	return nil
}
