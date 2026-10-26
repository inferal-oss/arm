package kubeprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

// TestMain makes the current test executable a real, short-lived helper
// process for the two early-exit IPC cases. The production helper is still
// exercised by the actual ARM binary tests in owned_exec_test.go.
func TestMain(m *testing.M) {
	mode := os.Getenv("ARM_KUBE_EARLY_EXIT_MODE")
	if mode != "" && len(os.Args) == 2 && (os.Args[1] == ExecHelperArgument || os.Args[1] == ForwardHelperArgument) {
		if mode == "before_hello" {
			os.Exit(125)
		}
		if mode == "before_result" {
			request := os.NewFile(3, "fixture-request")
			control := os.NewFile(4, "fixture-control")
			reply := os.NewFile(5, "fixture-reply")
			if request == nil || control == nil || reply == nil {
				os.Exit(125)
			}
			raw, err := io.ReadAll(io.LimitReader(request, 65537))
			var envelope struct {
				Nonce string `json:"nonce"`
			}
			if err != nil || json.Unmarshal(raw, &envelope) != nil || len(envelope.Nonce) != 64 {
				os.Exit(125)
			}
			if err := writeBoundedHelperJSON(reply, helperPacket{Version: 1, Nonce: envelope.Nonce, PID: os.Getpid(), PGID: os.Getpid()}, maxHelperPacket); err != nil {
				os.Exit(125)
			}
			var grant [1]byte
			if _, err := io.ReadFull(control, grant[:]); err != nil || grant[0] != 'G' {
				os.Exit(125)
			}
			os.Exit(125)
		}
		os.Exit(125)
	}
	os.Exit(m.Run())
}

func earlyExitPlan(t *testing.T) (Plan, Pod) {
	t.Helper()
	p, plan, _ := fixture(t)
	record, err := p.Apply(t.Context(), plan)
	if err != nil || record.Pod == nil {
		t.Fatalf("fixture Pod: %v", err)
	}
	return plan, *record.Pod
}

func TestOwnedStreamEarlyChildExitWithoutDeadline(t *testing.T) {
	image, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	plan, pod := earlyExitPlan(t)
	for _, mode := range []string{"before_hello", "before_result"} {
		for _, kind := range []string{"exec", "forward"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				t.Setenv("ARM_KUBE_EARLY_EXIT_MODE", mode)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				returned := make(chan error, 1)
				go func() {
					if kind == "exec" {
						command, _ := fixedExecCommand(plan, pod, execLookupBinary)
						runner := &OwnedExec{Plan: plan, image: image}
						returned <- runner.Exec(ctx, pod, command, nil, &bytes.Buffer{}, &bytes.Buffer{})
						return
					}
					_, err := startOwnedForwardImage(ctx, image, plan, pod)
					returned <- err
				}()
				select {
				case err := <-returned:
					if err == nil || errors.Is(err, context.Canceled) {
						t.Fatalf("early child exit was not observed: %v", err)
					}
				case <-time.After(3 * time.Second):
					cancel()
					select {
					case <-returned:
					case <-time.After(3 * time.Second):
					}
					t.Fatal("early child exit did not produce EOF without a deadline")
				}
			})
		}
	}
}
