package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config 是 agent 的配置。字段名与 config.json 一一对应。
type Config struct {
	Portal             string `json:"portal"`
	User               string `json:"user"`
	AuthMode           int    `json:"authmode"`
	Pool               string `json:"pool"`
	ISPID              int    `json:"isp_id"`
	PxyAcct            string `json:"pxyacct"`
	ExpectIPPrefix     string `json:"expected_ip_prefix"`
	IntervalSeconds    int    `json:"interval_seconds"`
	MaxBackoffSeconds  int    `json:"max_backoff_seconds"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	ConnectivityURL    string `json:"connectivity_url"`
	ConnectivityExpect string `json:"connectivity_expect"`
	// ConnectivityRetries：单轮内连通性检查的重试次数（抗瞬时抖动）。
	ConnectivityRetries int `json:"connectivity_retries"`
	// NetFailThreshold：logined==1 但连通性连续失败多少轮后才判定为掉线。
	// 用于避免因一次网络抖动而发起不必要的重登（重登会挤掉另一台设备）。
	NetFailThreshold int   `json:"net_fail_threshold"`
	MachineScope     bool  `json:"machine_scope"`
	LogMaxBytes      int64 `json:"log_max_bytes"`
}

func defaultConfig() Config {
	return Config{
		Portal:              "http://10.20.33.101",
		User:                "",
		AuthMode:            0,
		Pool:                "",
		ISPID:               0,
		PxyAcct:             "",
		ExpectIPPrefix:      "10.112.",
		IntervalSeconds:     30,
		MaxBackoffSeconds:   300,
		TimeoutSeconds:      10,
		ConnectivityURL:     "http://www.msftconnecttest.com/connecttest.txt",
		ConnectivityExpect:  "Microsoft Connect Test",
		ConnectivityRetries: 3,
		NetFailThreshold:    2,
		MachineScope:        true,
		LogMaxBytes:         5 << 20,
	}
}

// loadConfig 读取配置；文件不存在时返回默认值。
// 未在文件中出现的字段保持默认值（先填默认再 Unmarshal）。
func loadConfig(path string) (Config, error) {
	c := defaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, nil
}

func saveConfig(path string, c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}
