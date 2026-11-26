package imagegen

import "testing"

func TestImageRequestLimits(t *testing.T) {
	if err := Validate(512, 512, 20, 42); err != nil {
		t.Fatal(err)
	}
	for _, v := range [][3]int{{2048, 2048, 20}, {513, 512, 20}, {512, 512, 0}, {512, 512, 101}, {0, 512, 20}} {
		if err := Validate(v[0], v[1], v[2], 42); err == nil {
			t.Fatalf("accepted invalid configuration %v", v)
		}
	}
}
