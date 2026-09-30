//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// 纯标准库调用 Windows DPAPI（crypt32.dll），不引入 x/sys 依赖。
var (
	crypt32dll         = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtect   = crypt32dll.NewProc("CryptProtectData")
	procCryptUnprotect = crypt32dll.NewProc("CryptUnprotectData")
	kernel32dll        = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree      = kernel32dll.NewProc("LocalFree")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(d []byte) dataBlob {
	if len(d) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(d)), pbData: &d[0]}
}

func (b dataBlob) bytes() []byte {
	if b.cbData == 0 || b.pbData == nil {
		return nil
	}
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

const (
	cryptProtectUIForbidden  = 0x1
	cryptProtectLocalMachine = 0x4
)

// DPAPIProtect 使用 DPAPI 加密。machineScope=true 时绑定本机（SYSTEM 任务可解密）。
func DPAPIProtect(plain []byte, machineScope bool) ([]byte, error) {
	in := newBlob(plain)
	var out dataBlob

	var flags uintptr = cryptProtectUIForbidden
	if machineScope {
		flags |= cryptProtectLocalMachine
	}

	r, _, err := procCryptProtect.Call(
		uintptr(unsafe.Pointer(&in)),  // pDataIn
		0,                             // szDataDescr
		0,                             // pOptionalEntropy
		0,                             // pvReserved
		0,                             // pPromptStruct
		flags,                         // dwFlags
		uintptr(unsafe.Pointer(&out)), // pDataOut
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData 失败: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

// DPAPIUnprotect 解密 DPAPI 密文。
func DPAPIUnprotect(enc []byte) ([]byte, error) {
	in := newBlob(enc)
	var out dataBlob

	r, _, err := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&in)),  // pDataIn
		0,                             // ppszDataDescr
		0,                             // pOptionalEntropy
		0,                             // pvReserved
		0,                             // pPromptStruct
		0,                             // dwFlags
		uintptr(unsafe.Pointer(&out)), // pDataOut
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData 失败: %w（凭据可能由其他机器/用户加密，或任务运行身份与之不匹配）", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}
