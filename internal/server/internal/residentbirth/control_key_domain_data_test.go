package residentbirth

import "testing"

// Same independent static DATA vectors as the service-local key-domain helper.
// This checks exact bytes/domain/UTF-8 without a service -> birth import cycle.
func TestP1FooterKeyDomainConformsToServiceData(t *testing.T) {
	for key, want := range map[string]string{
		"synthetic-data-key":  "0b2f0003e4ccb3559d9c657e9bf68832ac9e2263bd021b1aba150a2be0ffa680",
		"synthetic-data-key ": "58932bf26ffc7bbe8cc9906320d948ab4e8440d549ada8437a5d6667ec344070",
		"synthetic-κ\x01":     "f362412b990897710ac4836a17ff8f19075fcea236eeac86fadec7b14e77cce3",
	} {
		got, err := P1LaunchKeyCommitmentData(key)
		if err != nil || got != want {
			t.Fatal("footer domain/raw UTF-8 DATA changed", got, err)
		}
	}
}
