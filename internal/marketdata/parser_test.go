package marketdata

import "testing"

func TestParser_RejectNaNInf(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "nan", value: "NaN"},
		{name: "pos_inf", value: "+Inf"},
		{name: "neg_inf", value: "-Inf"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := getFloat(map[string]interface{}{"open": tc.value}, "open")
			if err == nil {
				t.Fatalf("expected error for %s", tc.value)
			}
		})
	}
}
