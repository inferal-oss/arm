package server

import (
	"context"
	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
	"testing"
)

// Pure constructor/copy/cancellation DATA. No grant, image, namespace, original
// descriptor, Start or parent transfer is issued or invoked by this control.
func TestComposedParentPendingPrivateCommandSettingsCopyAndCancelledBeforeOpen(t *testing.T) {
	settings := daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0", StoragePools: []model.StoragePoolConfig{{Name: "data", Path: "/synthetic/pool", Tier: "fast"}}}
	root, err := newResidentParentPending("/synthetic", "parser-data-key", settings)
	if err != nil {
		t.Fatal(err)
	}
	settings.StoragePools[0].Path = "/changed"
	if root.settings.StoragePools[0].Path != "/synthetic/pool" || root.command == nil || len(root.command.ExtraFiles) != 13 || root.command.Env != nil || root.command.Process != nil {
		t.Fatal("pure root did not retain isolated settings/private command")
	}
	for _, file := range root.command.ExtraFiles {
		if file != nil {
			t.Fatal("pure constructor produced inherited native reference")
		}
	}
	if root.debit != nil || root.pin != nil || root.image != nil || root.fence != nil || root.process != nil {
		t.Fatal("pure constructor issued a component/native result")
	}
	copied := *root
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = copied.prepareOriginalBirth(ctx); err != errResidentParentOriginal {
		t.Fatal("copied parent accepted", err)
	}
	if root.prepareTried {
		t.Fatal("copy changed original parent")
	}
	if err = root.prepareOriginalBirth(ctx); err != context.Canceled {
		t.Fatal(err)
	}
	state := root.service.InspectOriginal()
	if state.OpenAttempted || state.OpenedFDs != 0 || root.debit != nil || root.startAttempted || root.transferWritten || root.firstError != context.Canceled {
		t.Fatal("cancelled parent entered native grant/birth path")
	}
	if err = root.launchAndTransferOriginal(ctx); err != errResidentParentOriginal || root.launchTried || root.command.Process != nil {
		t.Fatal("failed original replaced or launched", err)
	}
}
