package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// portalUA 与抓包中 Via 的 UA 不同（那是安卓端）。此处使用 Windows 桌面 UA。
const portalUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"

type PortalClient struct {
	cfg  Config
	http *http.Client
}

func NewPortalClient(cfg Config) (*PortalClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		// Proxy 显式置为 nil：直连门户，绕开系统代理（例如 Reqable / 抓包工具）。
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
	}
	return &PortalClient{
		cfg: cfg,
		http: &http.Client{
			Timeout:   time.Duration(cfg.TimeoutSeconds) * time.Second,
			Jar:       jar,
			Transport: tr,
		},
	}, nil
}

func (c *PortalClient) setPortalHeaders(req *http.Request) {
	req.Header.Set("User-Agent", portalUA)
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", c.cfg.Portal)
	req.Header.Set("Referer", c.cfg.Portal+"/")
}

// post 以 x-www-form-urlencoded 提交。body 为空串时会发出 Content-Length: 0，
// 与抓包中 ip.php / logoff.php 的形态一致。
func (c *PortalClient) post(path, body string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, c.cfg.Portal+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setPortalHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s 返回 HTTP %d", path, resp.StatusCode)
	}
	return b, nil
}

type PortalStatus struct {
	Ret  int `json:"ret"`
	Data struct {
		ProxyMode      int    `json:"proxymode"`
		IP             string `json:"ip"`
		Acct           string `json:"acct"`
		Logined        int    `json:"logined"`
		SessionLogined int    `json:"sessionlogined"`
	} `json:"data"`
	Msg string `json:"msg"`
}

func (c *PortalClient) Status() (*PortalStatus, error) {
	b, err := c.post("/api/ip.php", "")
	if err != nil {
		return nil, err
	}
	var st PortalStatus
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("解析 ip.php 响应失败: %w（原文: %s）", err, truncate(string(b), 200))
	}
	if st.Ret != 0 {
		return nil, fmt.Errorf("ip.php 返回 ret=%d msg=%s", st.Ret, st.Msg)
	}
	return &st, nil
}

// ProbeResult 是单个探针的结果。
type ProbeResult struct {
	Name   string
	OK     bool
	Status int
	Detail string
}

// CheckProbes 并发执行全部探针，返回成功个数与明细。
//
// 判定为"通"的条件（二者之一）：
//  1. HTTP 200，且 Expect 为空或正文包含 Expect（忽略大小写）；
//  2. HTTP 3xx，且 Location 不指向门户 —— 正常 HTTPS 跳转算通，
//     门户劫持的跳转目标是门户自身，会被判为不通。
func (c *PortalClient) CheckProbes() (int, []ProbeResult) {
	probes := c.cfg.Probes
	results := make([]ProbeResult, len(probes))

	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Add(1)
		go func(i int, p Probe) {
			defer wg.Done()
			results[i] = c.runProbe(p)
		}(i, p)
	}
	wg.Wait()

	okCount := 0
	for _, r := range results {
		if r.OK {
			okCount++
		}
	}
	return okCount, results
}

func (c *PortalClient) runProbe(p Probe) ProbeResult {
	res := ProbeResult{Name: p.Name}

	timeout := c.cfg.ProbeTimeoutSeconds
	if timeout <= 0 {
		timeout = 8
	}
	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
		Transport: &http.Transport{
			Proxy:             nil, // 直连；不读系统代理
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // 不跟随，便于识别劫持
		},
	}

	req, err := http.NewRequest(http.MethodGet, p.URL, nil)
	if err != nil {
		res.Detail = "构造请求失败: " + err.Error()
		return res
	}
	req.Header.Set("User-Agent", portalUA)
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := client.Do(req)
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	body := string(b)
	res.Status = resp.StatusCode

	// 3xx：只要不跳到门户就算通
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc == "" {
			res.Detail = fmt.Sprintf("HTTP %d 但无 Location", resp.StatusCode)
			return res
		}
		if strings.Contains(loc, c.cfg.Portal) || strings.Contains(loc, hostOf(c.cfg.Portal)) {
			res.Detail = "被重定向到门户: " + truncate(loc, 80)
			return res
		}
		res.OK = true
		res.Detail = fmt.Sprintf("HTTP %d -> %s", resp.StatusCode, truncate(loc, 60))
		return res
	}

	if resp.StatusCode != http.StatusOK {
		res.Detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return res
	}
	if p.Expect != "" && !strings.Contains(strings.ToLower(body), strings.ToLower(p.Expect)) {
		res.Detail = "正文不含 " + p.Expect + "（疑似门户劫持）: " + truncate(strings.TrimSpace(body), 60)
		return res
	}
	res.OK = true
	res.Detail = "ok"
	return res
}

// hostOf 从 URL 中取出主机名（不含协议与路径）。
func hostOf(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return s
}

// SummarizeProbes 生成简短的探针结果摘要，用于日志。
func SummarizeProbes(results []ProbeResult) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		if r.OK {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s(%s)", r.Name, r.Detail))
	}
	if len(parts) == 0 {
		return "全部通过"
	}
	return "未通过: " + strings.Join(parts, "; ")
}

type LoginResult struct {
	LoginRet int
	LoginMsg string
	AckRet   int
	StatRet  int
	StatMsg  string
}

// Login 复刻抓包还原的认证时序：ip.php → login.php → ack_auth.php → stat.php
func (c *PortalClient) Login(passHex string) (*LoginResult, error) {
	// 1) 先探活，让服务端下发会话 Cookie，建立会话
	_, _ = c.Status()

	// 字段顺序与抓包一致：user & pass & authmode & pool & isp_id & pxyacct
	body := "user=" + url.QueryEscape(c.cfg.User) +
		"&pass=" + passHex +
		"&authmode=" + strconv.Itoa(c.cfg.AuthMode) +
		"&pool=" + url.QueryEscape(c.cfg.Pool) +
		"&isp_id=" + strconv.Itoa(c.cfg.ISPID) +
		"&pxyacct=" + url.QueryEscape(c.cfg.PxyAcct)

	res := &LoginResult{}

	b, err := c.post("/api/login.php", body)
	if err != nil {
		return nil, fmt.Errorf("login.php 请求失败: %w", err)
	}
	var lr struct {
		Ret int    `json:"ret"`
		Msg string `json:"msg"`
	}
	if err := json.Unmarshal(b, &lr); err != nil {
		return nil, fmt.Errorf("解析 login.php 响应失败: %w（原文: %s）", err, truncate(string(b), 200))
	}
	res.LoginRet, res.LoginMsg = lr.Ret, lr.Msg
	if lr.Ret != 0 {
		return res, nil
	}

	// 2) AFF_ACK_AUTH
	if b2, err := c.post("/api/ack_auth.php", body); err == nil {
		var ar struct {
			Ret int `json:"ret"`
		}
		if json.Unmarshal(b2, &ar) == nil {
			res.AckRet = ar.Ret
		}
	}

	// 3) 状态确认（前端同样是轮询此接口直到 ret==0）
	if b3, err := c.post("/api/stat.php", body); err == nil {
		var sr struct {
			Ret int    `json:"ret"`
			Msg string `json:"msg"`
		}
		if json.Unmarshal(b3, &sr) == nil {
			res.StatRet, res.StatMsg = sr.Ret, sr.Msg
		}
	}
	return res, nil
}

// campusIPv4 返回本机符合指定前缀的 IPv4 地址；没有则返回空串。
func campusIPv4(prefix string) string {
	if prefix == "" {
		return ""
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip4 := ipnet.IP.To4(); ip4 != nil && strings.HasPrefix(ip4.String(), prefix) {
			return ip4.String()
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

var errNoIP = errors.New("no campus ip")
