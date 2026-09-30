package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Probe 是一个连通性探针。
//
// 判定为"通"的条件（二者之一）：
//  1. HTTP 200，且 Expect 为空或正文包含 Expect（忽略大小写）；
//  2. HTTP 3xx（301/302/303/307/308），且 Location 不指向门户 ——
//     正常的 HTTPS 跳转算通，而门户劫持的跳转目标是门户自身，会被判为不通。
//
// 之所以要接纳 3xx：实测部分站点（如必应）会间歇性地把 HTTP 跳转到 HTTPS。
type Probe struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Expect string `json:"expect"`
}

// Config 是 agent 的配置，字段名与 config.json 一一对应。
//
// 注意：Password 以明文保存在 config.json 中。该文件已列入 .gitignore，
// 请勿提交、上传，或把工作目录整个分享出去。
type Config struct {
	Portal   string `json:"portal"`
	User     string `json:"user"`
	Password string `json:"password"`

	AuthMode int    `json:"authmode"`
	Pool     string `json:"pool"`
	ISPID    int    `json:"isp_id"`
	PxyAcct  string `json:"pxyacct"`

	ExpectIPPrefix    string `json:"expected_ip_prefix"`
	IntervalSeconds   int    `json:"interval_seconds"`
	MaxBackoffSeconds int    `json:"max_backoff_seconds"`
	TimeoutSeconds    int    `json:"timeout_seconds"`

	// Probes：多个不同厂商的连通性探针。
	// 单一探针容易被"目标站点本身在本网络不可达"误伤
	// （实测 www.msftconnecttest.com 在校园网连不通，会导致误判掉线并反复重登），
	// 因此改为多探针投票。
	Probes []Probe `json:"probes"`
	// ProbeThreshold：至少多少个探针成功才算网络正常。
	ProbeThreshold int `json:"probe_threshold"`
	// ProbeTimeoutSeconds：单个探针的超时。
	ProbeTimeoutSeconds int `json:"probe_timeout_seconds"`

	// NetFailThreshold：探针不达标、但门户接口的 logined 仍为 1 时，
	// 连续多少轮才判定为"会话僵死"并重登。
	// 用于抑制瞬时抖动造成的误重登 —— 误重登会挤掉使用者另一台设备。
	NetFailThreshold int   `json:"net_fail_threshold"`
	LogMaxBytes      int64 `json:"log_max_bytes"`
}

// defaultProbes 选的都是「本网络实测可达、且正文稳定」的普通 HTTP 端点。
//
// 不用 DoH（dns.alidns.com / doh.pub）：实测在校园网内全部连不通，
// 即使关闭 TUN、DNS 恢复正常后依然不通，不适合当探针。
func defaultProbes() []Probe {
	return []Probe{
		{Name: "百度", URL: "http://www.baidu.com/robots.txt", Expect: "Baiduspider"},
		{Name: "必应", URL: "http://www.bing.com/robots.txt", Expect: "msnbot"},
		{Name: "阿里云", URL: "http://mirrors.aliyun.com/robots.txt", Expect: "User-agent"},
		{Name: "腾讯云", URL: "http://cloud.tencent.com/robots.txt", Expect: "tencent"},
	}
}

func defaultConfig() Config {
	return Config{
		Portal:              "http://10.20.33.101",
		AuthMode:            0,
		ISPID:               0,
		ExpectIPPrefix:      "10.112.",
		IntervalSeconds:     30,
		MaxBackoffSeconds:   300,
		TimeoutSeconds:      10,
		Probes:              defaultProbes(),
		ProbeThreshold:      3,
		ProbeTimeoutSeconds: 8,
		NetFailThreshold:    2,
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
	// 允许配置文件省略 probes，但不能写成空数组把探针全废掉
	if len(c.Probes) == 0 {
		c.Probes = defaultProbes()
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
