package psql

import (
	"reflect"
	"testing"
)

func TestStringArrayValue(t *testing.T) {
	tests := []struct {
		name string
		in   stringArray
		want string
	}{
		{"empty", stringArray{}, `{}`},
		{"single", stringArray{"tx-1"}, `{"tx-1"}`},
		{"many", stringArray{"a", "b", "c"}, `{"a","b","c"}`},
		{"escaped quote", stringArray{`he said "hi"`}, `{"he said \"hi\""}`},
		{"escaped backslash", stringArray{`a\b`}, `{"a\\b"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.in.Value()
			if err != nil {
				t.Fatalf("Value() returned error: %s", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStringArrayValue_Nil(t *testing.T) {
	var a stringArray
	got, err := a.Value()
	if err != nil {
		t.Fatalf("Value() returned error: %s", err)
	}
	if got != nil {
		t.Fatalf("expected nil driver.Value for nil slice, got %v", got)
	}
}

func TestInt64ArrayScan(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want []int64
	}{
		{"empty braces", "{}", nil},
		{"single", "{42}", []int64{42}},
		{"many", "{0,1,2,3}", []int64{0, 1, 2, 3}},
		{"negative", "{-7,7}", []int64{-7, 7}},
		{"bytes input", []byte("{1,2}"), []int64{1, 2}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got int64Array
			if err := got.Scan(tc.in); err != nil {
				t.Fatalf("Scan returned error: %s", err)
			}
			if !reflect.DeepEqual([]int64(got), tc.want) {
				t.Fatalf("got %v, want %v", []int64(got), tc.want)
			}
		})
	}
}

func TestInt64ArrayScan_Errors(t *testing.T) {
	tests := []struct {
		name string
		in   any
	}{
		{"not an array literal", "1,2,3"},
		{"bad number", "{1,abc}"},
		{"wrong type", 42},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got int64Array
			if err := got.Scan(tc.in); err == nil {
				t.Fatalf("expected error, got nil; result=%v", got)
			}
		})
	}
}

func TestInt64ArrayScan_Nil(t *testing.T) {
	got := int64Array{1, 2, 3}
	if err := got.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) returned error: %s", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}
