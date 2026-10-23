// Package providerproto implements the pure, bounded resource-provider wire
// contract. It does not execute code, register providers, authorize callers,
// reserve capacity, or establish external readiness or reclamation.
package providerproto

import "fmt"

const (
	Protocol         = "arm.resource-provider"
	Version          = 1
	MaxRequestBytes  = 64 << 10
	MaxResponseBytes = 256 << 10
	MaxDataBytes     = 16 << 10
	MaxDepth         = 16
	MaxArray         = 256
	MaxMembers       = 64
	MaxNodes         = 4096 // values, containers AND decoded object keys
	MaxStringBytes   = 4096
)

type ID string
type Username string
type Name string
type Key string
type Digest string
type Text string
type Uint string
type Timestamp string
type Method string

const (
	Describe  Method = "describe"
	Validate  Method = "validate"
	Plan      Method = "plan"
	Create    Method = "create"
	Lookup    Method = "lookup"
	Renew     Method = "renew"
	Release   Method = "release"
	Reconcile Method = "reconcile"
)

func (m Method) query() bool    { return m == Lookup || m == Reconcile }
func (m Method) mutation() bool { return m == Create || m == Renew || m == Release }

type Scope struct {
	Kind   string `json:"kind"`
	HostID ID     `json:"host_id,omitempty"`
}
type ExternalScope struct {
	Account   ID `json:"account"`
	Project   ID `json:"project"`
	Namespace ID `json:"namespace"`
}
type Registration struct {
	ID                    ID            `json:"id"`
	Generation            Uint          `json:"generation"`
	CodeSHA256            Digest        `json:"code_sha256"`
	ImplementationVersion ID            `json:"implementation_version"`
	ResourceType          Name          `json:"resource_type"`
	ConfigSHA256          Digest        `json:"config_sha256"`
	ExternalScope         ExternalScope `json:"external_scope"`
}
type Context struct {
	Authority    ID        `json:"authority"`
	Scope        Scope     `json:"scope"`
	OwnerUID     Uint      `json:"owner_uid"`
	OwnerName    *Username `json:"owner_name"`
	Agent        *ID       `json:"agent"`
	Session      *ID       `json:"session"`
	Project      *Text     `json:"project"`
	Reason       Text      `json:"reason"`
	OperationKey Key       `json:"operation_key"`
	IntentSHA256 Digest    `json:"intent_sha256"`
}
type Budget struct {
	Unit  ID   `json:"unit"`
	Limit Uint `json:"limit"`
}
type Identity struct {
	ExternalScope ExternalScope `json:"external_scope"`
	ID            ID            `json:"id"`
	Incarnation   ID            `json:"incarnation"`
}

type Params interface{ providerParams() }
type DescribeParams struct{}
type ValidateParams struct{}
type PlanParams struct {
	Specification Data      `json:"specification"`
	Budgets       []Budget  `json:"budgets"`
	ExpiresAt     Timestamp `json:"expires_at"`
}
type CreateParams struct {
	Specification       Data       `json:"specification"`
	SpecificationSHA256 Digest     `json:"specification_sha256"`
	Budgets             []Budget   `json:"budgets"`
	ExpiresAt           Timestamp  `json:"expires_at"`
	Dependencies        []Identity `json:"dependencies"`
}
type QueryParams struct {
	TargetMethod       Method    `json:"target_method"`
	ExternalIdentity   *Identity `json:"external_identity"`
	PriorReceiptSHA256 *Digest   `json:"prior_receipt_sha256"`
}
type RenewParams struct {
	ExternalIdentity   Identity  `json:"external_identity"`
	PriorReceiptSHA256 Digest    `json:"prior_receipt_sha256"`
	ExpectedRevision   Uint      `json:"expected_revision"`
	ExpiresAt          Timestamp `json:"expires_at"`
}
type ReleaseParams struct {
	ExternalIdentity   Identity `json:"external_identity"`
	PriorReceiptSHA256 Digest   `json:"prior_receipt_sha256"`
	ExpectedRevision   Uint     `json:"expected_revision"`
	Action             string   `json:"action"`
}

func (DescribeParams) providerParams() {}
func (ValidateParams) providerParams() {}
func (PlanParams) providerParams()     {}
func (CreateParams) providerParams()   {}
func (QueryParams) providerParams()    {}
func (RenewParams) providerParams()    {}
func (ReleaseParams) providerParams()  {}

type Request struct {
	Protocol       string       `json:"protocol"`
	Version        int          `json:"version"`
	Kind           string       `json:"kind"`
	RequestID      ID           `json:"request_id"`
	Registration   Registration `json:"registration"`
	Configuration  Data         `json:"configuration"`
	CredentialRefs []string     `json:"credential_refs"`
	Method         Method       `json:"method"`
	Context        Context      `json:"context"`
	Params         Params       `json:"params"`
}
type Result interface{ providerResult() }
type Descriptor struct {
	Kind                      string   `json:"kind"`
	ImplementationID          ID       `json:"implementation_id"`
	ImplementationVersion     ID       `json:"implementation_version"`
	ProtocolVersions          []int    `json:"protocol_versions"`
	ResourceTypes             []Name   `json:"resource_types"`
	Methods                   []Method `json:"methods"`
	DurableCreateDedup        bool     `json:"durable_create_dedup"`
	DurableTerminalTombstones bool     `json:"durable_terminal_tombstones"`
	KeyLookup                 bool     `json:"key_lookup"`
	ImmutableIdentity         bool     `json:"immutable_identity"`
	ReadOnlyReconcile         bool     `json:"read_only_reconcile"`
	Metadata                  Data     `json:"metadata"`
}
type Validated struct {
	Kind                string `json:"kind"`
	ConfigurationSHA256 Digest `json:"configuration_sha256"`
}
type Planned struct {
	Kind                string   `json:"kind"`
	SpecificationSHA256 Digest   `json:"specification_sha256"`
	EstimatedBudgets    []Budget `json:"estimated_budgets"`
	Readiness           string   `json:"readiness"`
	Metadata            Data     `json:"metadata"`
}
type Evidence struct {
	Format ID     `json:"format"`
	Data   Data   `json:"data"`
	SHA256 Digest `json:"sha256"`
}
type OperationReceipt struct {
	Kind                string    `json:"kind"`
	ProviderRequestID   ID        `json:"provider_request_id"`
	State               string    `json:"state"`
	ExternalIdentity    *Identity `json:"external_identity"`
	Revision            Uint      `json:"revision"`
	SpecificationSHA256 Digest    `json:"specification_sha256"`
	PriorReceiptSHA256  *Digest   `json:"prior_receipt_sha256"`
	Evidence            Evidence  `json:"evidence"`
	Metadata            Data      `json:"metadata"`
}

func (Descriptor) providerResult()       {}
func (Validated) providerResult()        {}
func (Planned) providerResult()          {}
func (OperationReceipt) providerResult() {}

type ProtocolError struct {
	Code              string `json:"code"`
	Message           Text   `json:"message"`
	RetryAfterSeconds *Uint  `json:"retry_after_seconds"`
}
type Response struct {
	Protocol     string         `json:"protocol"`
	Version      int            `json:"version"`
	Kind         string         `json:"kind"`
	RequestID    ID             `json:"request_id"`
	Registration Registration   `json:"registration"`
	Method       Method         `json:"method"`
	OperationKey Key            `json:"operation_key"`
	IntentSHA256 Digest         `json:"intent_sha256"`
	Result       Result         `json:"result"`
	Error        *ProtocolError `json:"error"`
}

// Data owns immutable canonical JSON. No numbers, NUL or malformed Unicode are
// permitted. Zero Data is uninitialized; use NullData for observed null.
type Data struct{ canonical string }

func NullData() Data { return Data{canonical: "null"} }
func ParseData(raw []byte) (Data, error) {
	n, err := parse(raw, MaxRequestBytes)
	if err != nil {
		return Data{}, err
	}
	return dataNode(n)
}
func (d Data) Canonical() []byte { return []byte(d.canonical) }
func (d Data) SHA256() Digest    { return digest([]byte(d.canonical)) }
func (d Data) MarshalJSON() ([]byte, error) {
	if d.canonical == "" {
		return nil, fmt.Errorf("uninitialized provider data")
	}
	return d.Canonical(), nil
}

// Decoding produces explicitly untrusted values. Taking a copy is not authority.
type DecodedRequest struct{ request Request }
type DecodedResponse struct{ response Response }

func (d DecodedRequest) Request() Request    { return cloneRequest(d.request) }
func (d DecodedResponse) Response() Response { return cloneResponse(d.response) }
