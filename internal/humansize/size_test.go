package humansize

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestBytesBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		size uint64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024*1024 - 1, "1024.0 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{827367424, "789.0 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TiB"},
		{math.MaxUint64, "16.0 EiB"},
	}
	for _, test := range tests {
		test := test
		t.Run(strconv.FormatUint(test.size, 10), func(t *testing.T) {
			t.Parallel()
			if got := Bytes(test.size); got != test.want {
				t.Fatalf("Bytes(%d) = %q, want %q", test.size, got, test.want)
			}
		})
	}
}

func TestBytesAlwaysProducesOneBoundedLine(t *testing.T) {
	t.Parallel()

	for _, size := range []uint64{0, 1023, 1024, 1 << 20, 1 << 30, 1 << 60, math.MaxUint64} {
		got := Bytes(size)
		if got == "" || strings.ContainsAny(got, "\r\n\t") {
			t.Fatalf("Bytes(%d) produced unsafe diagnostic text %q", size, got)
		}
		if len(got) > 32 {
			t.Fatalf("Bytes(%d) produced unexpectedly long text %q", size, got)
		}
	}
}

func FuzzBytesProducesIECOutput(f *testing.F) {
	for _, size := range []uint64{0, 1, 1023, 1024, 827367424, 1 << 30, math.MaxUint64} {
		f.Add(size)
	}
	f.Fuzz(func(t *testing.T, size uint64) {
		got := Bytes(size)
		if got == "" || !strings.Contains(got, "B") || strings.ContainsAny(got, "\r\n") {
			t.Fatalf("Bytes(%d) = %q", size, got)
		}
	})
}
