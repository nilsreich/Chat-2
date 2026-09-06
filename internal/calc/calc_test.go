package calc

import (
	"math"
	"testing"
)

func p(v int) *int { return &v }
func TestCalculate(t *testing.T) {
	cases := []struct {
		name   string
		in     []Item
		ww, ow float64
		want   *float64
	}{
		{"weighted", []Item{{p(10), "written", 2, false}, {p(12), "test", 1, false}}, 50, 50, pf(10.6666667)},
		{"combined", []Item{{p(10), "written", 2, false}, {p(12), "test", 1, false}, {p(13), "oral", 1, false}, {p(11), "oral", 1, false}}, 50, 50, pf(11.3333333)},
		{"sixty forty", []Item{{p(10), "written", 1, false}, {p(15), "oral", 1, false}}, 60, 40, pf(12)},
		{"only oral", []Item{{p(0), "oral", 1, false}, {nil, "written", 1, false}, {p(4), "oral", 1, true}}, 50, 50, pf(0)},
		{"none", nil, 50, 50, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Calculate(c.in, c.ww, c.ow)
			if c.want == nil {
				if r.Overall != nil {
					t.Fatal("wanted nil")
				}
			} else if r.Overall == nil || math.Abs(*r.Overall-*c.want) > 1e-5 {
				t.Fatalf("got %v", r.Overall)
			}
		})
	}
}
func pf(v float64) *float64 { return &v }
