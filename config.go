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

// defaultProbes 是默认探针集：4 家厂商的普通 HTTP 端点 + 2 家厂商的 DoH(HTTPS)。
//
// 为何混用两种协议：
//   - HTTP 探针能被门户劫持"看见"（劫持会返回 3xx 到门户或门户页面），
//     用于判定"认证态是否已失效"；
//   - DoH 探针走 HTTPS，门户无法劫持，用于判定"链路本身是否通"，
//     同时顺带验证 DNS 可用性。
//
// 关于阿里云 DoH 的传参（易错点）：它有两套接口，参数不同 ——
//   - https://dns.alidns.com/dns-query   需要 RFC 8484 的 dns=<base64url 二进制报文>
//   - https://dns.alidns.com/resolve     才是 JSON 风格，接受 name=/type=
//     并要求请求头 accept: application/dns-json
//
// 本探针用的是后者。IP 形式 https://223.5.5.5/resolve 同样可用
// （阿里证书 SAN 中含该 IP），此处不重复计入。
func defaultProbes() []Probe {
	return []Probe{
		{Name: "百度", URL: "http://www.baidu.com/robots.txt", Expect: "Baiduspider"},
		{Name: "必应", URL: "http://www.bing.com/robots.txt", Expect: "msnbot"},
		{Name: "阿里云", URL: "http://mirrors.aliyun.com/robots.txt", Expect: "User-agent"},
		{Name: "腾讯云", URL: "http://cloud.tencent.com/robots.txt", Expect: "tencent"},
		{Name: "阿里DoH", URL: "https://dns.alidns.com/resolve?name=www.baidu.com&type=A", Expect: `"Status":0`},
		{Name: "腾讯DoH", URL: "https://doh.pub/dns-query?name=www.baidu.com&type=A", Expect: `"Status":0`},
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
		ProbeThreshold:      4, // 6 个探针（4 家厂商）里至少 4 个通，约等于"4 家里至少 3 家可用"
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
