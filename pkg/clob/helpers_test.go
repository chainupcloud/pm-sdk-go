package clob

import (
	"bytes"
	"testing"
)

func TestNormalizeECDSAv(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []byte
	}{
		{
			name: "v=0 加 27",
			in:   sigWithV(0),
			want: sigWithV(27),
		},
		{
			name: "v=1 加 27",
			in:   sigWithV(1),
			want: sigWithV(28),
		},
		{
			name: "v=27 不变",
			in:   sigWithV(27),
			want: sigWithV(27),
		},
		{
			name: "v=28 不变",
			in:   sigWithV(28),
			want: sigWithV(28),
		},
		{
			name: "长度非 65 原样返回",
			in:   []byte{0x01, 0x02, 0x03},
			want: []byte{0x01, 0x02, 0x03},
		},
		{
			name: "nil 原样返回",
			in:   nil,
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeECDSAv(tt.in)
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("normalizeECDSAv(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeECDSAv_NotMutateInput(t *testing.T) {
	in := sigWithV(0)
	orig := make([]byte, 65)
	copy(orig, in)
	_ = normalizeECDSAv(in)
	if !bytes.Equal(in, orig) {
		t.Fatalf("normalizeECDSAv 不应修改入参；got %v, want %v", in, orig)
	}
}

func sigWithV(v byte) []byte {
	sig := make([]byte, 65)
	for i := 0; i < 64; i++ {
		sig[i] = byte(i + 1)
	}
	sig[64] = v
	return sig
}
