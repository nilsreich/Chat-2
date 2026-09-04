package calc

import "fmt"

type Item struct {
	Points *int
	Type   string
	Weight float64
	Absent bool
}
type Result struct{ Written, Oral, Overall *float64 }

func Calculate(items []Item, writtenWeight, oralWeight float64) Result {
	var ws, ww, os, ow float64
	for _, i := range items {
		if i.Points == nil || i.Absent {
			continue
		}
		if i.Type == "oral" {
			os += float64(*i.Points) * i.Weight
			ow += i.Weight
		} else {
			ws += float64(*i.Points) * i.Weight
			ww += i.Weight
		}
	}
	var r Result
	if ww > 0 {
		v := ws / ww
		r.Written = &v
	}
	if ow > 0 {
		v := os / ow
		r.Oral = &v
	}
	if r.Written != nil && r.Oral != nil {
		v := (*r.Written*writtenWeight + *r.Oral*oralWeight) / (writtenWeight + oralWeight)
		r.Overall = &v
	} else if r.Written != nil {
		v := *r.Written
		r.Overall = &v
	} else if r.Oral != nil {
		v := *r.Oral
		r.Overall = &v
	}
	return r
}
func Format(v *float64) string {
	if v == nil {
		return "–"
	}
	return fmt.Sprintf("%.1f", *v)
}
