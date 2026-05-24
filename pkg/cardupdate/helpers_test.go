package cardupdate

import "testing"

func TestUnwrapValue(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  any
	}{
		{"int array [3.0]", []any{float64(3)}, 3},
		{"float array [2.5]", []any{2.5}, 2.5},
		{"string array [text]", []any{"text"}, "text"},
		{"bool array [true]", []any{true}, true},
		{"plain int", 42, 42},
		{"plain string", "hello", "hello"},
		{"plain nil", nil, nil},
		{"zero float [0.0]", []any{float64(0)}, 0},
		{"negative [-1.0]", []any{float64(-1)}, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UnwrapValue(tt.input)
			if got != tt.want {
				t.Errorf("UnwrapValue(%v) = %v (%T), want %v (%T)", tt.input, got, got, tt.want, tt.want)
			}
		})
	}

	// Test multi-element and empty arrays separately (slices aren't comparable with !=).
	t.Run("multi-element array", func(t *testing.T) {
		input := []any{1, 2}
		got := UnwrapValue(input)
		arr, ok := got.([]any)
		if !ok || len(arr) != 2 || arr[0] != 1 || arr[1] != 2 {
			t.Errorf("UnwrapValue(%v) = %v (%T), want []any{1,2}", input, got, got)
		}
	})
	t.Run("empty array", func(t *testing.T) {
		input := []any{}
		got := UnwrapValue(input)
		arr, ok := got.([]any)
		if !ok || len(arr) != 0 {
			t.Errorf("UnwrapValue(%v) = %v (%T), want empty []any", input, got, got)
		}
	})
}

func TestConvertCharValue(t *testing.T) {
	tests := []struct {
		name        string
		generated   string
		currentJSON string
		want        any
	}{
		{
			name:        "string to number (current is int)",
			generated:   "42",
			currentJSON: "[42]",
			want:        42,
		},
		{
			name:        "string to float (current is float)",
			generated:   "2.5",
			currentJSON: "[2.5]",
			want:        2.5,
		},
		{
			name:        "string to string array (current is string)",
			generated:   "красный",
			currentJSON: `["синий"]`,
			want:        []string{"красный"},
		},
		{
			name:        "comma-separated to string array",
			generated:   "красный, синий",
			currentJSON: `["синий"]`,
			want:        []string{"красный", "синий"},
		},
		{
			name:        "number to string array (current is array)",
			generated:   "XL",
			currentJSON: `["S","M"]`,
			want:        []string{"XL"},
		},
		{
			name:        "fallback on invalid JSON",
			generated:   "value",
			currentJSON: "{invalid}",
			want:        []string{"value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConvertCharValue(tt.generated, tt.currentJSON)
			switch want := tt.want.(type) {
			case int:
				if g, ok := got.(int); !ok || g != want {
					t.Errorf("ConvertCharValue() = %v (%T), want %v (%T)", got, got, want, want)
				}
			case float64:
				if g, ok := got.(float64); !ok || g != want {
					t.Errorf("ConvertCharValue() = %v (%T), want %v (%T)", got, got, want, want)
				}
			case []string:
				g, ok := got.([]string)
				if !ok {
					t.Errorf("ConvertCharValue() = %v (%T), want []string", got, got)
					return
				}
				if len(g) != len(want) {
					t.Errorf("ConvertCharValue() = %v, want %v", g, want)
					return
				}
				for i := range g {
					if g[i] != want[i] {
						t.Errorf("ConvertCharValue()[%d] = %q, want %q", i, g[i], want[i])
					}
				}
			}
		})
	}
}

func TestStringToCharArray(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"single value", "красный", []string{"красный"}},
		{"two values", "красный, синий", []string{"красный", "синий"}},
		{"with spaces", " a , b , c ", []string{"a", "b", "c"}},
		{"empty string", "", []string{""}},
		{"trailing comma", "a,", []string{"a"}},
		{"multiple commas", "a,,b", []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StringToCharArray(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("StringToCharArray(%q) = %v, want %v", tt.input, got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("StringToCharArray(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}
