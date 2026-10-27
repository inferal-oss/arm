package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

type kubeRuntimeIdentity struct {
	Host       string
	Generation string
	CPU        string
	Memory     string
	Disk       string
}

var kubeRuntimeID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var kubeRuntimeToken = regexp.MustCompile(`^[a-f0-9]{64}$`)

func verifiedLocalKubeRuntime(ctx context.Context, home, expectedURL string) (kubeRuntimeIdentity, error) {
	ep, err := local.ReadEndpoint(home)
	if err != nil || ep.URL != expectedURL || !kubeRuntimeID.MatchString(ep.Generation) || !kubeRuntimeToken.MatchString(ep.Token) || ep.PID < 1 {
		return kubeRuntimeIdentity{}, errors.New("exact local Kubernetes ARM endpoint unavailable")
	}
	// The private token belongs only to this exact loopback endpoint. A
	// redirect, proxy or shared default client must not move its proof request
	// to another listener, even under the same hostname.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	conn, _, err := websocket.Dial(ctx, ep.URL, &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Authorization": []string{"Bearer " + ep.Token}}})
	if err != nil {
		return kubeRuntimeIdentity{}, errors.New("authenticated Kubernetes ARM runtime unavailable")
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)
	id := local.ID()
	if err := wsjson.Write(ctx, conn, model.WireRequest{Version: 1, ID: id, Operation: "status", Params: json.RawMessage(`{}`)}); err != nil {
		return kubeRuntimeIdentity{}, err
	}
	var response struct {
		Version int             `json:"version"`
		ID      string          `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *model.Error    `json:"error"`
	}
	if err := wsjson.Read(ctx, conn, &response); err != nil || response.Version != 1 || response.ID != id || response.Error != nil {
		return kubeRuntimeIdentity{}, errors.New("authenticated Kubernetes ARM status unavailable")
	}
	var status model.Status
	decoder := json.NewDecoder(bytes.NewReader(response.Result))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil {
		return kubeRuntimeIdentity{}, errors.New("invalid Kubernetes ARM status")
	}
	if status.Owner != strconv.Itoa(os.Geteuid()) || !kubeRuntimeID.MatchString(status.Host) || status.Draining || status.CPU == nil || status.CPU.Ceiling == nil || *status.CPU.Ceiling <= 0 || status.Memory == nil || status.Memory.Ceiling == nil || *status.Memory.Ceiling <= 0 || status.Disk == nil || status.Disk.Ceiling == nil || *status.Disk.Ceiling <= 0 {
		return kubeRuntimeIdentity{}, errors.New("Kubernetes ARM runtime identity or reviewed ceilings unavailable")
	}
	after, err := local.ReadEndpoint(home)
	if err != nil || after != ep {
		return kubeRuntimeIdentity{}, errors.New("Kubernetes ARM endpoint changed during status proof")
	}
	return kubeRuntimeIdentity{
		Host: status.Host, Generation: ep.Generation,
		CPU:    strconv.FormatFloat(*status.CPU.Ceiling, 'g', -1, 64),
		Memory: strconv.FormatInt(*status.Memory.Ceiling, 10),
		Disk:   strconv.FormatInt(*status.Disk.Ceiling, 10),
	}, nil
}

// This fixed helper is invoked only from the exact shipped binary after the
// start marker. It authenticates to the Pod's private local backend and emits
// no token, capacity claim, mesh certificate or SSH key.
func kubeRuntimeProofMain(args []string, out io.Writer) int {
	if len(args) != 0 {
		return 125
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	identity, err := verifiedLocalKubeRuntime(ctx, "/arm/private/state", "ws://127.0.0.1:9443/v1/ws")
	if err != nil {
		return 1
	}
	if _, err := fmt.Fprintf(out, "ARM_RUNTIME_VERIFIED %s %s %s %s %s\n", identity.Host, identity.Generation, identity.CPU, identity.Memory, identity.Disk); err != nil {
		return 1
	}
	return 0
}
