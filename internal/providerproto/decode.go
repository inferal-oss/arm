package providerproto

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

var dataType = reflect.TypeFor[Data]()

func assign(n node, v reflect.Value) error {
	if v.Type() == dataType {
		d, e := dataNode(n)
		if e != nil {
			return e
		}
		v.Set(reflect.ValueOf(d))
		return nil
	}
	if v.Kind() == reflect.Pointer {
		if n.kind == 'n' {
			v.SetZero()
			return nil
		}
		v.Set(reflect.New(v.Type().Elem()))
		return assign(n, v.Elem())
	}
	switch v.Kind() {
	case reflect.Struct:
		if n.kind != 'o' {
			return fmt.Errorf("provider object required")
		}
		known := map[string]int{}
		optional := map[string]bool{}
		for i := range v.NumField() {
			f := v.Type().Field(i)
			parts := strings.Split(f.Tag.Get("json"), ",")
			if parts[0] == "" || parts[0] == "-" {
				return fmt.Errorf("unsupported provider field")
			}
			known[parts[0]] = i
			optional[parts[0]] = len(parts) > 1 && parts[1] == "omitempty"
		}
		for k, x := range n.object {
			i, ok := known[k]
			if !ok {
				return fmt.Errorf("unknown provider field %q", k)
			}
			if e := assign(x, v.Field(i)); e != nil {
				return fmt.Errorf("%s: %w", k, e)
			}
		}
		for k := range known {
			if _, ok := n.object[k]; !ok && !optional[k] {
				return fmt.Errorf("missing provider field %q", k)
			}
		}
	case reflect.Slice:
		if n.kind != 'a' {
			return fmt.Errorf("provider array required")
		}
		v.Set(reflect.MakeSlice(v.Type(), len(n.array), len(n.array)))
		for i, x := range n.array {
			if e := assign(x, v.Index(i)); e != nil {
				return e
			}
		}
	case reflect.String:
		if n.kind != 's' {
			return fmt.Errorf("provider string required")
		}
		if e := stringType(n.text, v.Type()); e != nil {
			return e
		}
		v.SetString(n.text)
	case reflect.Int:
		if n.kind != 'd' || n.text != "1" {
			return fmt.Errorf("only literal protocol version 1 is supported")
		}
		v.SetInt(1)
	case reflect.Bool:
		if n.kind != 't' && n.kind != 'f' {
			return fmt.Errorf("provider boolean required")
		}
		v.SetBool(n.kind == 't')
	default:
		return fmt.Errorf("unsupported provider wire type")
	}
	return nil
}
func stringType(s string, t reflect.Type) error {
	switch t {
	case reflect.TypeFor[Username]():
		if len(s) < 1 || len(s) > 256 {
			return fmt.Errorf("provider username UTF-8 byte length")
		}
	case reflect.TypeFor[ID]():
		if len(s) < 1 || len(s) > 256 {
			return fmt.Errorf("provider ID length")
		}
		for _, c := range []byte(s) {
			if c < '!' || c > '~' {
				return fmt.Errorf("provider IDs require printable ASCII")
			}
		}
	case reflect.TypeFor[Key]():
		if len(s) < 1 || len(s) > 128 {
			return fmt.Errorf("provider key length")
		}
		for _, c := range []byte(s) {
			if c < '!' || c > '~' {
				return fmt.Errorf("invalid provider key")
			}
		}
	case reflect.TypeFor[Name]():
		if len(s) < 1 || len(s) > 64 || s[0] < 'a' || s[0] > 'z' || s[len(s)-1] == '-' {
			return fmt.Errorf("invalid provider type name")
		}
		for _, c := range []byte(s) {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return fmt.Errorf("invalid provider type name")
			}
		}
	case reflect.TypeFor[Digest]():
		if len(s) != 64 {
			return fmt.Errorf("provider digest length")
		}
		for _, c := range []byte(s) {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return fmt.Errorf("invalid provider digest")
			}
		}
	case reflect.TypeFor[Uint]():
		_, e := uintValue(s)
		return e
	case reflect.TypeFor[Timestamp]():
		v, e := time.Parse(time.RFC3339Nano, s)
		if len(s) > 40 || e != nil || v.Format(time.RFC3339Nano) != s || !strings.HasSuffix(s, "Z") {
			return fmt.Errorf("canonical UTC provider timestamp required")
		}
	case reflect.TypeFor[Method]():
		if !validMethod(Method(s)) {
			return fmt.Errorf("unsupported provider method")
		}
	}
	return nil
}
func validMethod(m Method) bool {
	switch m {
	case Describe, Validate, Plan, Create, Lookup, Renew, Release, Reconcile:
		return true
	}
	return false
}
func objectFields(n node, names ...string) error {
	if n.kind != 'o' || len(n.object) != len(names) {
		return fmt.Errorf("closed provider envelope required")
	}
	for _, s := range names {
		if _, ok := n.object[s]; !ok {
			return fmt.Errorf("missing provider envelope field %q", s)
		}
	}
	return nil
}
func field(n node, name string, out any) error {
	return assign(n.object[name], reflect.ValueOf(out).Elem())
}
func DecodeRequest(raw []byte) (DecodedRequest, error) {
	n, e := parse(raw, MaxRequestBytes)
	if e != nil {
		return DecodedRequest{}, e
	}
	return decodeRequest(n)
}
func decodeRequest(n node) (DecodedRequest, error) {
	if e := objectFields(n, "protocol", "version", "kind", "request_id", "registration", "configuration", "credential_refs", "method", "context", "params"); e != nil {
		return DecodedRequest{}, e
	}
	var q Request
	for _, x := range []struct {
		name string
		out  any
	}{{"protocol", &q.Protocol}, {"version", &q.Version}, {"kind", &q.Kind}, {"request_id", &q.RequestID}, {"registration", &q.Registration}, {"configuration", &q.Configuration}, {"credential_refs", &q.CredentialRefs}, {"method", &q.Method}, {"context", &q.Context}} {
		if e := field(n, x.name, x.out); e != nil {
			return DecodedRequest{}, e
		}
	}
	var e error
	switch q.Method {
	case Describe:
		var x DescribeParams
		e = field(n, "params", &x)
		q.Params = x
	case Validate:
		var x ValidateParams
		e = field(n, "params", &x)
		q.Params = x
	case Plan:
		var x PlanParams
		e = field(n, "params", &x)
		q.Params = x
	case Create:
		var x CreateParams
		e = field(n, "params", &x)
		q.Params = x
	case Lookup, Reconcile:
		var x QueryParams
		e = field(n, "params", &x)
		q.Params = x
	case Renew:
		var x RenewParams
		e = field(n, "params", &x)
		q.Params = x
	case Release:
		var x ReleaseParams
		e = field(n, "params", &x)
		q.Params = x
	}
	if e == nil {
		e = shapeRequest(q)
	}
	if e != nil {
		return DecodedRequest{}, e
	}
	return DecodedRequest{q}, nil
}
func shapeRegistration(r Registration) error {
	if r.Generation == "0" {
		return fmt.Errorf("positive provider generation required")
	}
	return nil
}
func shapeRequest(q Request) error {
	if q.Protocol != Protocol || q.Version != Version || q.Kind != "request" {
		return fmt.Errorf("unsupported provider request envelope")
	}
	if e := shapeRegistration(q.Registration); e != nil {
		return e
	}
	if len(q.CredentialRefs) != 0 {
		return fmt.Errorf("provider credentials unsupported")
	}
	if strings.TrimSpace(string(q.Context.Reason)) == "" {
		return fmt.Errorf("provider reason required")
	}
	if q.Context.Scope.Kind == "host" {
		if q.Context.Scope.HostID == "" {
			return fmt.Errorf("provider host scope requires host ID")
		}
	} else if q.Context.Scope.Kind != "global" || q.Context.Scope.HostID != "" {
		return fmt.Errorf("invalid provider scope")
	}
	checkBudgets := func(v []Budget) error {
		if len(v) > 64 {
			return fmt.Errorf("provider budget limit")
		}
		seen := map[ID]bool{}
		for _, b := range v {
			if seen[b.Unit] {
				return fmt.Errorf("duplicate provider budget unit")
			}
			seen[b.Unit] = true
		}
		return nil
	}
	switch x := q.Params.(type) {
	case DescribeParams:
		if q.Method != Describe {
			return fmt.Errorf("wrong provider params")
		}
	case ValidateParams:
		if q.Method != Validate {
			return fmt.Errorf("wrong provider params")
		}
	case PlanParams:
		if q.Method != Plan {
			return fmt.Errorf("wrong provider params")
		}
		return checkBudgets(x.Budgets)
	case CreateParams:
		if q.Method != Create || len(x.Dependencies) > 64 {
			return fmt.Errorf("wrong provider create params")
		}
		if e := checkBudgets(x.Budgets); e != nil {
			return e
		}
		seen := map[Identity]bool{}
		for _, d := range x.Dependencies {
			if seen[d] {
				return fmt.Errorf("duplicate provider dependency")
			}
			seen[d] = true
		}
	case QueryParams:
		if !q.Method.query() || !x.TargetMethod.mutation() {
			return fmt.Errorf("invalid provider query target")
		}
	case RenewParams:
		if q.Method != Renew || x.ExpectedRevision == "0" {
			return fmt.Errorf("invalid provider renewal")
		}
	case ReleaseParams:
		if q.Method != Release || x.Action != "delete" || x.ExpectedRevision == "0" {
			return fmt.Errorf("invalid provider release")
		}
	default:
		return fmt.Errorf("unsupported provider parameter type")
	}
	return nil
}
func DecodeResponse(raw []byte) (DecodedResponse, error) {
	n, e := parse(raw, MaxResponseBytes)
	if e != nil {
		return DecodedResponse{}, e
	}
	if e = objectFields(n, "protocol", "version", "kind", "request_id", "registration", "method", "operation_key", "intent_sha256", "result", "error"); e != nil {
		return DecodedResponse{}, e
	}
	var r Response
	for _, x := range []struct {
		name string
		out  any
	}{{"protocol", &r.Protocol}, {"version", &r.Version}, {"kind", &r.Kind}, {"request_id", &r.RequestID}, {"registration", &r.Registration}, {"method", &r.Method}, {"operation_key", &r.OperationKey}, {"intent_sha256", &r.IntentSHA256}, {"error", &r.Error}} {
		if e = field(n, x.name, x.out); e != nil {
			return DecodedResponse{}, e
		}
	}
	if n.object["result"].kind != 'n' {
		switch r.Method {
		case Describe:
			var x Descriptor
			e = field(n, "result", &x)
			r.Result = x
		case Validate:
			var x Validated
			e = field(n, "result", &x)
			r.Result = x
		case Plan:
			var x Planned
			e = field(n, "result", &x)
			r.Result = x
		default:
			var x OperationReceipt
			e = field(n, "result", &x)
			r.Result = x
		}
	}
	if e == nil {
		e = shapeResponse(r)
	}
	if e != nil {
		return DecodedResponse{}, e
	}
	return DecodedResponse{r}, nil
}
func shapeResponse(r Response) error {
	if r.Protocol != Protocol || r.Version != Version || r.Kind != "response" {
		return fmt.Errorf("unsupported provider response envelope")
	}
	if e := shapeRegistration(r.Registration); e != nil {
		return e
	}
	if (r.Error == nil) == (r.Result == nil) {
		return fmt.Errorf("provider response needs exactly one result or error")
	}
	if r.Error != nil {
		switch r.Error.Code {
		case "unsupported", "unavailable", "invalid_configuration", "invalid_specification", "conflict", "denied", "not_found", "failed", "internal":
			return nil
		default:
			return fmt.Errorf("unknown provider error code")
		}
	}
	switch x := r.Result.(type) {
	case Descriptor:
		if x.Kind != "descriptor" || r.Method != Describe || len(x.ProtocolVersions) != 1 || x.ProtocolVersions[0] != 1 || len(x.ResourceTypes) < 1 || len(x.ResourceTypes) > 64 || len(x.Methods) < 1 || len(x.Methods) > 8 {
			return fmt.Errorf("invalid provider descriptor")
		}
		types := map[Name]bool{}
		for _, v := range x.ResourceTypes {
			if types[v] {
				return fmt.Errorf("duplicate provider type")
			}
			types[v] = true
		}
		methods := map[Method]bool{}
		for _, v := range x.Methods {
			if methods[v] {
				return fmt.Errorf("duplicate provider method")
			}
			methods[v] = true
		}
	case Validated:
		if x.Kind != "validated" || r.Method != Validate {
			return fmt.Errorf("invalid provider validation result")
		}
	case Planned:
		if x.Kind != "planned" || r.Method != Plan || len(x.EstimatedBudgets) > 64 {
			return fmt.Errorf("invalid provider plan")
		}
		if x.Readiness != "immediate" && x.Readiness != "delayed" && x.Readiness != "unavailable" {
			return fmt.Errorf("unknown provider readiness")
		}
		seen := map[ID]bool{}
		for _, b := range x.EstimatedBudgets {
			if seen[b.Unit] {
				return fmt.Errorf("duplicate provider estimate unit")
			}
			seen[b.Unit] = true
		}
	case OperationReceipt:
		if x.Kind != "operation" || !(r.Method.mutation() || r.Method.query()) {
			return fmt.Errorf("invalid provider operation result")
		}
		switch x.State {
		case "pending", "ready", "ending", "released", "failed", "unknown", "no_effect":
		default:
			return fmt.Errorf("unknown provider operation state")
		}
		if (x.State == "ready" || x.State == "ending" || x.State == "released") && (x.ExternalIdentity == nil || x.Revision == "0") {
			return fmt.Errorf("provider result lacks immutable identity/revision")
		}
	default:
		return fmt.Errorf("unsupported provider result type")
	}
	return nil
}

// Check caller-built values before encoding/json can replace malformed Unicode
// or invoke a user-defined interface implementation. No callback types allowed.
func quotedBytes(s string) int {
	n := 2
	for _, r := range s {
		switch r {
		case '"', '\\', '\b', '\t', '\n', '\f', '\r':
			n += 2
		default:
			if r < 32 {
				n += 6
			} else {
				n += utf8.RuneLen(r)
			}
		}
	}
	return n
}
func checkGo(v reflect.Value, depth int, nodes *int, size *int, max int) error {
	if v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if !v.IsNil() {
			return checkGo(v.Elem(), depth, nodes, size, max)
		}
		if depth > MaxDepth || *nodes >= MaxNodes {
			return fmt.Errorf("provider typed value limit")
		}
		*nodes++
		*size += 4
		if *size > max {
			return fmt.Errorf("provider typed byte limit")
		}
		return nil
	}
	if depth > MaxDepth || *nodes >= MaxNodes || *size > max {
		return fmt.Errorf("provider typed value limit")
	}
	*nodes++
	if v.Type() == dataType {
		d := v.Interface().(Data)
		if d.canonical == "" {
			return fmt.Errorf("uninitialized provider data")
		}
		*size += len(d.canonical)
	} else {
		switch v.Kind() {
		case reflect.Struct:
			*size += 2
			members := 0
			for i := range v.NumField() {
				parts := strings.Split(v.Type().Field(i).Tag.Get("json"), ",")
				if len(parts) > 1 && parts[1] == "omitempty" && v.Field(i).IsZero() {
					continue
				}
				*nodes++
				if members > 0 {
					*size++
				}
				members++
				*size += quotedBytes(parts[0]) + 1
				if e := checkGo(v.Field(i), depth+1, nodes, size, max); e != nil {
					return e
				}
			}
		case reflect.Slice:
			if v.Len() > MaxArray {
				return fmt.Errorf("provider typed array limit")
			}
			if v.IsNil() {
				*size += 4
			} else {
				*size += 2
				if v.Len() > 0 {
					*size += v.Len() - 1
				}
				for i := range v.Len() {
					if e := checkGo(v.Index(i), depth+1, nodes, size, max); e != nil {
						return e
					}
				}
			}
		case reflect.String:
			x := v.String()
			if !utf8.ValidString(x) || strings.ContainsRune(x, 0) || len(x) > MaxStringBytes {
				return fmt.Errorf("invalid provider typed string")
			}
			*size += quotedBytes(x)
		case reflect.Int:
			if v.Int() != 1 {
				return fmt.Errorf("invalid provider typed protocol version")
			}
			*size++
		case reflect.Bool:
			if v.Bool() {
				*size += 4
			} else {
				*size += 5
			}
		default:
			return fmt.Errorf("unsupported provider typed value")
		}
	}
	if *size > max {
		return fmt.Errorf("provider typed byte limit")
	}
	return nil
}

func encode(v any, max int) ([]byte, error) {
	nodes, bytes := 0, 0
	if e := checkGo(reflect.ValueOf(v), 1, &nodes, &bytes, max); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	if len(raw) > 6*max {
		return nil, fmt.Errorf("provider encoded byte limit")
	}
	n, e := parse(raw, 6*max)
	if e != nil {
		return nil, e
	}
	b := canonical(n)
	if len(b) > max {
		return nil, fmt.Errorf("provider canonical byte limit")
	}
	return b, nil
}
func EncodeRequest(q Request) ([]byte, error) {
	if e := shapeRequest(q); e != nil {
		return nil, e
	}
	b, e := encode(q, MaxRequestBytes)
	if e != nil {
		return nil, e
	}
	_, e = DecodeRequest(b)
	return b, e
}
func EncodeResponse(r Response) ([]byte, error) {
	if e := shapeResponse(r); e != nil {
		return nil, e
	}
	b, e := encode(r, MaxResponseBytes)
	if e != nil {
		return nil, e
	}
	_, e = DecodeResponse(b)
	return b, e
}
func cloneValue(v reflect.Value) reflect.Value {
	if v.Type() == dataType {
		return v
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		x := reflect.New(v.Type()).Elem()
		x.Set(cloneValue(v.Elem()))
		return x
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		x := reflect.New(v.Type().Elem())
		x.Elem().Set(cloneValue(v.Elem()))
		return x
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		x := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			x.Index(i).Set(cloneValue(v.Index(i)))
		}
		return x
	case reflect.Struct:
		x := reflect.New(v.Type()).Elem()
		for i := range v.NumField() {
			x.Field(i).Set(cloneValue(v.Field(i)))
		}
		return x
	default:
		return v
	}
}
func cloneRequest(q Request) Request    { return cloneValue(reflect.ValueOf(q)).Interface().(Request) }
func cloneResponse(r Response) Response { return cloneValue(reflect.ValueOf(r)).Interface().(Response) }
