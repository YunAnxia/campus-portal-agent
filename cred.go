package main

import (
	"errors"
	"os"
)

// savePassword 以 DPAPI 加密后写入文件。
func savePassword(cfg Config, path string, plain []byte) error {
	enc, err := DPAPIProtect(plain, cfg.MachineScope)
	if err != nil {
		return err
	}
	return os.WriteFile(path, enc, 0o600)
}

// loadPassword 读取并解密密码，返回可清零的 []byte。
func loadPassword(cfg Config, path string) ([]byte, error) {
	enc, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("凭据文件不存在，请先执行: portalagent set-cred")
		}
		return nil, err
	}
	return DPAPIUnprotect(enc)
}
