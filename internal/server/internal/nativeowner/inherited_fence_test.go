package nativeowner

import (
	"testing"

	"getarm.org/internal/server/internal/residentbirth"
)

// Missing/occupied/zero original refusals precede the sole factory and Geteuid.
// No inherited/native descriptor, issuer Open, positive fence or callback fixture.
func TestInheritedServePendingRequiresRetainedEmptyDestinationAndOriginalFence(t *testing.T) {
	if err := NewInheritedServePending(nil, nil); err != errNotOriginal {
		t.Fatal(err)
	}
	occupied := &ServeIssuer{}
	original := occupied
	if err := NewInheritedServePending(&occupied, nil); err != errNotOriginal || occupied != original || occupied.owner != nil {
		t.Fatal("occupied pending replaced", err)
	}
	var empty *ServeIssuer
	if err := NewInheritedServePending(&empty, nil); err != errNotOriginal || empty != nil {
		t.Fatal("missing fence constructed issuer", err)
	}
	if err := NewInheritedServePending(&empty, &residentbirth.InheritedFencePending{}); err == nil || empty != nil {
		t.Fatal("zero fence constructed issuer", err)
	}
}

func TestInheritedPreparedViewsRequireSameActualIssuerAndFence(t *testing.T) {
	for _, issuer := range []*ServeIssuer{nil, {}, {owner: &nativeOwner{}}} {
		if _, err := issuer.ValidateInheritedOriginalViews(&ManagerView{}, &ServerView{}, &residentbirth.InheritedFencePending{}); err != errNotOriginal {
			t.Fatal(err)
		}
	}
}
