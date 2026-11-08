package observe

import "testing"

func TestParseLoadavg(t *testing.T) {
	load, err := parseLoadavg([]byte("3.50 2.00 1.00 4/900 12345\n"))
	if err != nil || load != [3]float64{3.5, 2, 1} {
		t.Fatalf("%v %v", load, err)
	}
	if _, err := parseLoadavg([]byte("x y")); err == nil {
		t.Fatal("accepted invalid loadavg")
	}
}
