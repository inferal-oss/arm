package buildinfo

import "testing"

func TestBundleTagChecksCompiledVersionAndProtocol(t *testing.T) {
	original := BundleTag
	t.Cleanup(func() { BundleTag = original })
	for _, tag := range []string{"", ExpectedBundleTag(1)} {
		BundleTag = tag
		if err := ValidateBundleTag(1); err != nil {
			t.Fatal(err)
		}
	}
	for _, tag := range []string{"arm-bundle-v1:another-version:1", ExpectedBundleTag(2)} {
		BundleTag = tag
		if ValidateBundleTag(1) == nil {
			t.Fatal("incorrect bundle annotation accepted")
		}
	}
}
