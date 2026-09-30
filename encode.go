package main

import (
	"crypto/aes"
	"encoding/hex"
	"errors"
	"math/rand"
)

// portalSaltCharset 与门户前端 assets/js/crypto.js 中 _0x345c[78] 完全一致。
// 前端取 Math.floor(Math.random() * 61)，即下标 0..60（注意：含 '8'，不含 '9'）。
const portalSaltCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="

// portalAESKey 是硬编码在前端 crypto.js 中的 16 字节密钥。
// 前端 CryptoJS.VDX 实为 AES-128-ECB + ZeroPadding。
var portalAESKey = []byte("5a3b9f207411a8ed")

// encryptPortalBlob 对已拼好的数据（4 字节盐 + 明文）做 AES-128-ECB + ZeroPadding，
// 返回 hex 小写密文。
//
// 单独拆出来是为了让测试能用固定输入与独立实现算出的向量比对，
// 而不必依赖 EncodePassword 的随机盐。
func encryptPortalBlob(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("输入为空")
	}

	// ZeroPadding：仅在未按块对齐时补零字节。
	// 与 CryptoJS.pad.ZeroPadding 一致 —— 恰好对齐时不追加整块，
	// 这正是它与 PKCS#7 最容易被搞混的区别。
	buf := make([]byte, len(data), len(data)+aes.BlockSize)
	copy(buf, data)
	if rem := len(buf) % aes.BlockSize; rem != 0 {
		buf = append(buf, make([]byte, aes.BlockSize-rem)...)
	}

	block, err := aes.NewCipher(portalAESKey)
	if err != nil {
		return "", err
	}
	ct := make([]byte, len(buf))
	for off := 0; off < len(buf); off += aes.BlockSize {
		block.Encrypt(ct[off:off+aes.BlockSize], buf[off:off+aes.BlockSize])
	}
	return hex.EncodeToString(ct), nil
}

// EncodePassword 复刻门户前端的 encode()：
//
//	salt   = 4 个随机字符（字符集与取值范围同前端）
//	明文   = salt + 密码
//	密文   = AES-128-ECB(ZeroPadding(明文), key) 的 hex 小写
//
// 明文以 []byte 传入，便于调用方在用完后清零。
func EncodePassword(plain []byte, rnd *rand.Rand) (string, error) {
	if len(plain) == 0 {
		return "", errors.New("密码为空")
	}

	salt := make([]byte, 4)
	for i := range salt {
		salt[i] = portalSaltCharset[rnd.Intn(61)]
	}

	data := make([]byte, 0, 4+len(plain))
	data = append(data, salt...)
	data = append(data, plain...)

	return encryptPortalBlob(data)
}

// DecodePassword 仅供测试/取证使用：还原 salt 与明文。
func DecodePassword(hexCipher string) ([]byte, error) {
	ct, err := hex.DecodeString(hexCipher)
	if err != nil {
		return nil, err
	}
	if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("密文长度不是块大小的整数倍")
	}
	block, err := aes.NewCipher(portalAESKey)
	if err != nil {
		return nil, err
	}
	pt := make([]byte, len(ct))
	for off := 0; off < len(ct); off += aes.BlockSize {
		block.Decrypt(pt[off:off+aes.BlockSize], ct[off:off+aes.BlockSize])
	}
	return pt, nil
}
