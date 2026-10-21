package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/cli"
)

func TestAgentMessageHelpOfflineScopesFlags(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, tt := range []struct {
		action          string
		present, absent []string
	}{
		{"send", []string{"--id", "--session", "--registration", "--to ", "--to-registration", "--text", "--key", "registered"}, []string{"--message ", "--after"}},
		{"reply", []string{"--id", "--session", "--registration", "--message", "--text", "--key", "recorded sender"}, []string{"--to ", "--to-registration", "--after"}},
		{"message-lookup", []string{"--id", "--session", "--registration", "--key", "without native delivery"}, []string{"--to ", "--message ", "--text", "--lease"}},
	} {
		t.Run(tt.action, func(t *testing.T) {
			var out, stderr bytes.Buffer
			code := cli.Main([]string{"--home", home, "agent", tt.action, "--help"}, strings.NewReader(""), &out, &stderr)
			if code != 0 || stderr.Len() != 0 {
				t.Fatalf("%d %s", code, &stderr)
			}
			for _, want := range tt.present {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %q: %s", want, &out)
				}
			}
			for _, unwanted := range tt.absent {
				if strings.Contains(out.String(), "\n  "+unwanted) {
					t.Errorf("irrelevant %q: %s", unwanted, &out)
				}
			}
		})
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("offline help created state", err)
	}
}
func TestAgentMessageSelectorsAndErrorsBeforeBackend(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	identity := []string{"--id", "sender", "--session", "b49339cf-a8fd-4a62-98d5-5d892be0589a", "--registration", "registration"}
	cases := [][]string{{"send", "--to", "recipient", "--text", "body"}, {"send", "--id", "sender", "--session", "invalid", "--registration", "registration", "--to", "recipient", "--text", "body"}, append([]string{"send"}, append(identity, "--to", "broadcast", "--text", "body")...), append([]string{"reply"}, append(identity, "--text", "body")...), append([]string{"message-lookup"}, identity...), append([]string{"send"}, append(identity, "--to", "operator", "--text", strings.Repeat("a", 16385))...), append([]string{"message-lookup"}, append(identity, "--key", strings.Repeat("k", 129))...)}
	for i, args := range cases {
		var out, stderr bytes.Buffer
		code := cli.Main(append([]string{"--home", home, "agent"}, args...), strings.NewReader(""), &out, &stderr)
		if code == 0 || out.Len() != 0 {
			t.Fatalf("case %d: code %d out %s err %s", i, code, &out, &stderr)
		}
		var result struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(stderr.Bytes(), &result); err != nil || result.Error.Code != "invalid_request" {
			t.Fatalf("case %d: %s %v", i, &stderr, err)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("invalid selector/request created state", err)
	}
}
