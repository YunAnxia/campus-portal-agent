package main

import (
	"math/rand"
	"strings"
	"testing"
)

// 以下两组测试向量由独立的 .NET AES 实现算出（不是本包代码的输出），
// 用于证明 AES-128-ECB + ZeroPadding 的语义与该门户前端一致
// —— 前端 crypto.js 中自注册的 CryptoJS.VDX 实为 AES-128-ECB + Zeros padding。
//
//	key = "5a3b9f207411a8ed"
//	"TESTp@ssw0rd-X"    14 字节，未对齐 → 补 2 个零字节
//	"TESTabcdefghijkl"  16 字节，恰好对齐 → 不补
const (
	vectorPaddedInput  = "TESTp@ssw0rd-X"
	vectorPaddedHex    = "79e2ce7633bf5285f103a958580b337c"
	vectorAlignedInput = "TESTabcdefghijkl"
	vectorAlignedHex   = "9256a50cf311dbe3c01fbaefc89b229b"
)

func TestEncryptMatchesIndependentVector(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{vectorPaddedInput, vectorPaddedHex},
		{vectorAlignedInput, vectorAlignedHex},
	} {
		got, err := encryptPortalBlob([]byte(tc.in))
		if err != nil {
			t.Fatalf("encryptPortalBlob(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("输入 %q\n  期望 %s\n  实际 %s", tc.in, tc.want, got)
		}
	}
}

// 恰好按块对齐时不应追加整块 —— 这是 ZeroPadding 与 PKCS#7 最容易被搞混之处。
func TestZeroPaddingDoesNotAddFullBlock(t *testing.T) {
	got, err := encryptPortalBlob([]byte(vectorAlignedInput))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 32 {
		t.Errorf("对齐输入应产生单块（32 个 hex 字符），实际 %d", len(got))
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	for _, pw := range []string{"a", "abcdefghijkl", "012345678901234567890123"} {
		rnd := rand.New(rand.NewSource(42))
		hexStr, err := EncodePassword([]byte(pw), rnd)
		if err != nil {
			t.Fatalf("EncodePassword(%q): %v", pw, err)
		}
		if len(hexStr)%32 != 0 {
			t.Errorf("密文长度应为 32 的整数倍，实际 %d", len(hexStr))
		}
		pt, err := DecodePassword(hexStr)
		if err != nil {
			t.Fatalf("DecodePassword: %v", err)
		}
		if got := strings.TrimRight(string(pt[4:]), "\x00"); got != pw {
			t.Errorf("往返不一致: 期望 %q，实际 %q", pw, got)
		}
		for _, ch := range string(pt[:4]) {
			if idx := strings.IndexRune(portalSaltCharset, ch); idx < 0 || idx > 60 {
				t.Errorf("盐字符 %q 超出前端取值范围 (idx=%d)", ch, idx)
			}
		}
	}
}

func TestEncodeEmptyRejected(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	if _, err := EncodePassword(nil, rnd); err == nil {
		t.Error("空密码应当返回错误")
	}
}

func TestEncodeDecodeAreInverse(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	h, err := EncodePassword([]byte("roundtrip"), rnd)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := DecodePassword(h)
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.TrimRight(string(pt[4:]), "\x00"); s != "roundtrip" {
		t.Errorf("期望 roundtrip，实际 %q", s)
	}
}
