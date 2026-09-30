package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const version = "1.0.0"

func defaultDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	d := filepath.Dir(exe)
	// go run 时二进制在临时目录，回退到当前目录
	if strings.Contains(strings.ToLower(d), "go-build") {
		if wd, err := os.Getwd(); err == nil {
			return wd
		}
	}
	return d
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "错误: "+format+"\n", a...)
	os.Exit(1)
}

func usage() {
	fmt.Print(`portalagent - 校园网门户自动重登保活代理

用法:
  portalagent <命令> [-dir 工作目录] [-user 账号] [-portal 门户地址]

命令:
  status      只读探测当前认证状态与外网连通性，不做任何认证动作（安全，可随时运行）
  once        执行一个周期：检测，必要时重新认证，然后退出
  run         常驻循环（默认命令；供计划任务/服务使用）
  set-cred    设置账号与密码（密码用 DPAPI 加密后写入 credential.dpapi）
  install     注册开机启动的计划任务（SYSTEM 身份，失败自动重启）
  uninstall   删除该计划任务
  taskxml     打印计划任务 XML（无法自动注册时可手动导入）
  decode <hex>  仅用于取证：解密一个门户 encode() 密文，显示盐与明文
  version     显示版本

工作目录下会生成:
  config.json        配置
  credential.dpapi   DPAPI 加密的密码
  agent.log          滚动日志

示例:
  portalagent set-cred
  portalagent status
  portalagent once
  portalagent install
`)
}

func main() {
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	fs := flag.NewFlagSet("portalagent", flag.ExitOnError)
	dir := fs.String("dir", defaultDir(), "工作目录")
	userFlag := fs.String("user", "", "覆盖配置中的账号")
	portalFlag := fs.String("portal", "", "覆盖配置中的门户地址")
	_ = fs.Parse(args)

	cfgPath := filepath.Join(*dir, "config.json")
	credPath := filepath.Join(*dir, "credential.dpapi")
	logPath := filepath.Join(*dir, "agent.log")

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fatal("读取配置失败: %v", err)
	}
	if *userFlag != "" {
		cfg.User = *userFlag
	}
	if *portalFlag != "" {
		cfg.Portal = strings.TrimRight(*portalFlag, "/")
	}

	lg := NewLogger(logPath, cfg.LogMaxBytes)

	switch cmd {
	case "status":
		cmdStatus(cfg)
	case "once":
		cmdOnce(cfg, credPath, lg)
	case "run":
		cmdRun(cfg, credPath, lg)
	case "set-cred":
		cmdSetCred(cfg, cfgPath, credPath, lg)
	case "install":
		cmdInstall(cfg, cfgPath, credPath, lg)
	case "uninstall":
		cmdUninstall(lg)
	case "taskxml":
		cmdTaskXML()
	case "decode":
		cmdDecode(args)
	case "version", "-v", "--version":
		fmt.Println("portalagent", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func cmdStatus(cfg Config) {
	if ip := campusIPv4(cfg.ExpectIPPrefix); ip != "" {
		fmt.Printf("本机校园网地址 : %s\n", ip)
	} else {
		fmt.Printf("本机校园网地址 : 未检测到（期望前缀 %q）\n", cfg.ExpectIPPrefix)
	}
	fmt.Printf("门户           : %s\n", cfg.Portal)

	c, err := NewPortalClient(cfg)
	if err != nil {
		fatal("初始化客户端失败: %v", err)
	}

	st, err := c.Status()
	if err != nil {
		fmt.Printf("ip.php 探测失败 : %v\n", err)
		return
	}
	b, _ := json.Marshal(st)
	fmt.Printf("POST /api/ip.php: %s\n", string(b))

	ok, detail := c.CheckInternet()
	fmt.Printf("外网连通       : %v（%s）\n", ok, detail)

	if st.Data.Logined == 1 && ok {
		fmt.Println("判定           : 状态正常，无需重登")
	} else {
		fmt.Println("判定           : 需要重新认证")
	}
}

type cycleResult struct {
	Kind   string
	IP     string
	Detail string
}

// agentState 跨轮次保存状态。
// netBadStreak 用于抑制"logined=1 但外网瞬时不通"造成的误重登 ——
// 误重登会挤掉使用者另一台设备，代价高于多等一轮。
type agentState struct {
	netBadStreak int
}

const (
	kindOK          = "ok"
	kindReloginOK   = "relogin-ok"
	kindReloginFail = "relogin-fail"
	kindUnstable    = "unstable"
	kindSkip        = "skip"
	kindError       = "error"
)

func runCycle(cfg Config, credPath string, lg *Logger, state *agentState) cycleResult {
	// 前置守卫：本机必须处于预期的校园网网段，避免在别的网络下误触发认证
	if campusIPv4(cfg.ExpectIPPrefix) == "" {
		return cycleResult{Kind: kindSkip,
			Detail: fmt.Sprintf("未检测到 %s 网段的本地地址，跳过本轮", cfg.ExpectIPPrefix)}
	}

	c, err := NewPortalClient(cfg)
	if err != nil {
		return cycleResult{Kind: kindError, Detail: "初始化客户端失败: " + err.Error()}
	}

	st, err := c.Status()
	if err != nil {
		return cycleResult{Kind: kindError, Detail: "探测 ip.php 失败: " + err.Error()}
	}

	netOK, netDetail := c.CheckInternet()

	threshold := cfg.NetFailThreshold
	if threshold < 1 {
		threshold = 1
	}

	if st.Data.Logined == 1 {
		if netOK {
			state.netBadStreak = 0
			return cycleResult{Kind: kindOK, IP: st.Data.IP}
		}
		// logined 说正常，但外网不通：可能是瞬时抖动，累计到阈值才动手
		state.netBadStreak++
		if state.netBadStreak < threshold {
			return cycleResult{Kind: kindUnstable, IP: st.Data.IP,
				Detail: fmt.Sprintf("logined=1 但外网不通（第 %d/%d 轮，%s），继续观察",
					state.netBadStreak, threshold, netDetail)}
		}
	} else {
		state.netBadStreak = 0
	}

	lg.Warnf("检测到掉线（ip=%s logined=%d 外网=%v %s），开始重新认证",
		st.Data.IP, st.Data.Logined, netOK, netDetail)

	plain, err := loadPassword(cfg, credPath)
	if err != nil {
		return cycleResult{Kind: kindError, Detail: "读取凭据失败: " + err.Error()}
	}
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	pass, encErr := EncodePassword(plain, rnd)
	for i := range plain {
		plain[i] = 0 // 尽力清零（Go 无法保证内存被彻底擦除）
	}
	if encErr != nil {
		return cycleResult{Kind: kindError, Detail: "计算口令密文失败: " + encErr.Error()}
	}

	lr, err := c.Login(pass)
	if err != nil {
		return cycleResult{Kind: kindReloginFail, Detail: err.Error()}
	}
	if lr.LoginRet != 0 {
		return cycleResult{Kind: kindReloginFail,
			Detail: fmt.Sprintf("login.php ret=%d msg=%q", lr.LoginRet, lr.LoginMsg)}
	}

	// 复核：新建会话再探一次，避免受本次登录会话的 sessionlogined 干扰
	c2, err := NewPortalClient(cfg)
	if err != nil {
		c2 = c
	}
	if st2, err2 := c2.Status(); err2 == nil && st2.Data.Logined == 1 {
		if ok2, _ := c2.CheckInternet(); ok2 {
			return cycleResult{Kind: kindReloginOK, IP: st2.Data.IP, Detail: lr.StatMsg}
		}
	}
	return cycleResult{Kind: kindReloginFail,
		Detail: fmt.Sprintf("认证后复核未通过（ack=%d stat=%d %q）", lr.AckRet, lr.StatRet, lr.StatMsg)}
}

func cmdOnce(cfg Config, credPath string, lg *Logger) {
	res := runCycle(cfg, credPath, lg, &agentState{})
	lg.Infof("单次执行: kind=%s ip=%s %s", res.Kind, res.IP, res.Detail)
}

func cmdRun(cfg Config, credPath string, lg *Logger) {
	lg.Infof("portalagent %s 启动（门户 %s，账号 %s，间隔 %ds）",
		version, cfg.Portal, cfg.User, cfg.IntervalSeconds)

	interval := cfg.IntervalSeconds
	lastOK := false
	state := &agentState{}

	for {
		res := runCycle(cfg, credPath, lg, state)

		switch res.Kind {
		case kindOK:
			if !lastOK {
				lg.Infof("状态正常（ip=%s logined=1）", res.IP)
			}
			lastOK = true
			interval = cfg.IntervalSeconds
		case kindUnstable:
			lg.Warnf("%s", res.Detail)
			lastOK = false
			interval = cfg.IntervalSeconds
		case kindReloginOK:
			lg.Infof("已重新认证成功（ip=%s %s）", res.IP, res.Detail)
			lastOK = true
			interval = cfg.IntervalSeconds
		case kindReloginFail:
			lg.Errorf("重新认证失败: %s", res.Detail)
			lastOK = false
			interval = nextBackoff(interval, cfg.MaxBackoffSeconds)
		case kindSkip:
			if lastOK {
				lg.Warnf("%s", res.Detail)
			}
			lastOK = false
			interval = nextBackoff(interval, cfg.MaxBackoffSeconds)
		default:
			lg.Errorf("%s", res.Detail)
			lastOK = false
			interval = nextBackoff(interval, cfg.MaxBackoffSeconds)
		}

		sleep := interval + rand.Intn(8) // 加抖动，避免与其他设备同步
		time.Sleep(time.Duration(sleep) * time.Second)
	}
}

func nextBackoff(cur, max int) int {
	if cur <= 0 {
		cur = 30
	}
	n := cur * 2
	if n > max {
		n = max
	}
	return n
}

func cmdSetCred(cfg Config, cfgPath, credPath string, lg *Logger) {
	if cfg.User == "" {
		fmt.Print("请输入校园网账号: ")
		rd := bufio.NewReader(os.Stdin)
		s, _ := rd.ReadString('\n')
		cfg.User = strings.TrimSpace(s)
		if cfg.User == "" {
			fatal("账号不能为空")
		}
	}
	fmt.Printf("账号: %s\n", cfg.User)
	fmt.Print("请输入校园网密码（输入不回显）: ")
	pw, err := readPassword()
	if err != nil {
		fatal("读取密码失败: %v", err)
	}
	if pw == "" {
		fatal("密码不能为空")
	}
	if err := savePassword(cfg, credPath, []byte(pw)); err != nil {
		fatal("保存凭据失败: %v", err)
	}
	pw = ""
	if err := saveConfig(cfgPath, cfg); err != nil {
		fatal("保存配置失败: %v", err)
	}
	fmt.Printf("已写入:\n  %s\n  %s\n", cfgPath, credPath)
	lg.Infof("凭据已更新（账号 %s，DPAPI machine_scope=%v）", cfg.User, cfg.MachineScope)
}

func cmdInstall(cfg Config, cfgPath, credPath string, lg *Logger) {
	if cfg.User == "" {
		fatal("配置中缺少账号，请先执行: portalagent set-cred")
	}
	if _, err := os.Stat(credPath); err != nil {
		fatal("凭据文件不存在，请先执行: portalagent set-cred")
	}
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if err := saveConfig(cfgPath, cfg); err != nil {
			fatal("写入配置失败: %v", err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		fatal("获取自身路径失败: %v", err)
	}
	if err := registerTask(exe, filepath.Dir(exe)); err != nil {
		fatal("%v", err)
	}
	fmt.Printf("已注册计划任务: %s（SYSTEM 身份 / 开机延迟 30s 启动 / 失败每分钟重启）\n", taskName)
	fmt.Printf("可执行文件: %s\n", exe)
	fmt.Println("建议在「任务计划程序」中右键该任务 →「运行」验证一次，或手动执行 portalagent.exe once")
	lg.Infof("已注册计划任务 %s -> %s", taskName, exe)
}

func cmdUninstall(lg *Logger) {
	if err := unregisterTask(); err != nil {
		fatal("%v", err)
	}
	fmt.Printf("已删除计划任务: %s\n", taskName)
	lg.Infof("已删除计划任务 %s", taskName)
}

// cmdTaskXML 打印计划任务定义，便于在无法自动注册时手动导入。
func cmdTaskXML() {
	exe, err := os.Executable()
	if err != nil {
		fatal("获取自身路径失败: %v", err)
	}
	fmt.Print(buildTaskXML(exe, filepath.Dir(exe)))
}

// cmdDecode 仅用于取证复核：解密前端 encode() 产出的密文。
func cmdDecode(args []string) {
	var hexStr string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			hexStr = a
			break
		}
	}
	if hexStr == "" {
		fatal("用法: portalagent decode <hex密文>")
	}
	pt, err := DecodePassword(hexStr)
	if err != nil {
		fatal("解密失败: %v", err)
	}
	if len(pt) < 4 {
		fatal("明文长度异常: %d", len(pt))
	}
	salt := string(pt[:4])
	body := strings.TrimRight(string(pt[4:]), "\x00")
	fmt.Printf("盐   : %s\n", salt)
	fmt.Printf("明文 : %s\n", body)
	fmt.Printf("长度 : %d（含盐）\n", len(pt))
}
