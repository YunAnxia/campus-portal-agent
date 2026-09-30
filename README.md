# portalagent —— 校园网门户自动重登保活代理

---

> ## ⚠️ 声明：本项目完全由 AI 生成
>
> 本仓库的全部内容 —— Go 源码、构建脚本、文档、git 提交 —— **均由 AI（DeepSeek Harness 中的编码代理）生成**。
> 人类只负责提出需求、提供实测数据，并对结果做取舍。
>
> **代码未经人工逐行审阅。** 请在使用前自行评估；因使用本工具产生的任何后果由使用者自负。

---

针对 `http://10.20.33.101` 的门户认证自动重登工具。
**用途：让本机在账号被其他设备挤下线后自动重新认证，从而保持可远程访问（RDP）。**

Go 1.23 编写，**纯标准库、零外部依赖**，产出单一静态 `portalagent.exe`。

相关文档：[ANALYSIS.md](ANALYSIS.md) —— 门户认证流程与设计依据的简述。

> **凭据以明文保存。** `config.json` 中含账号与密码明文，该文件已列入 `.gitignore`。
> 请勿提交、上传，或把工作目录整个分享出去。

---

## 0. 先读这一节：先试更省事的办法

在部署本工具之前，请先排除以下三种情况 —— 它们都能让脚本变得不必要：

1. **本机是否有多余网卡在占名额？**
   本机实测期间同时存在以太网与 WLAN 两块会拿 IP 的网卡。
   如果校园网按 MAC 计数并发，**一台机器就可能占掉两个名额**。
   → 用以太网时禁用 WLAN，很可能直接解决问题。

2. **是不是手机/平板在抢名额？**
   若踢人的来源是自己的另一台设备，减少竞争设备比写脚本更有效。

3. **是否只是空闲超时？**
   若如此，调策略或降低探测间隔即可，不需要完整重登。

另外建议先问网络中心有没有**正式途径**（设备 MAC 绑定、提高并发数、专用远程访问通道）。

## 1. 环境要求

- Windows 10/11（x64）
- Go 1.21+（仅构建需要；运行只需 `portalagent.exe`）
- 注册计划任务需要**管理员权限**

## 2. 快速开始

```powershell
# 1) 构建（仓库只含源码，需自行编译）
cd <仓库目录>
.\build.cmd          # 批处理版，不受 PowerShell 执行策略限制（推荐）
# 或：.\build.ps1    # 若被策略拦截，用 powershell -ExecutionPolicy Bypass -File .\build.ps1
# 或直接：go build -trimpath -ldflags "-s -w" -o portalagent.exe .

# 2) 生成配置并填入账号密码
Copy-Item .\config.example.json .\config.json
notepad .\config.json      # 至少填 user 与 password；网段不同则同步改 expected_ip_prefix

# 3) 放到固定位置，避免被移动后计划任务失效
New-Item -ItemType Directory -Force C:\ProgramData\CampusPortalAgent | Out-Null
Copy-Item .\portalagent.exe, .\config.json C:\ProgramData\CampusPortalAgent\

# 4) 验证（只读，不做任何认证动作，可随时安全运行）
cd C:\ProgramData\CampusPortalAgent
.\portalagent.exe status

# 5) 单次试跑（当前已认证时应输出 ok 且不发起登录）
.\portalagent.exe once

# 6) 注册开机自启的计划任务（需管理员）
.\portalagent.exe install
```

安装后在「任务计划程序」中找到 `CampusPortalAgent`，右键 →「运行」验证一次。

## 3. 命令

| 命令 | 说明 |
|---|---|
| `status` | **只读**。打印本机校园网地址、`ip.php` 返回、外网连通性与判定结果。不发起认证，可随时运行 |
| `once` | 执行一个周期：检测，必要时重登，然后退出。用于试跑 |
| `run` | 常驻循环（默认命令），供计划任务使用 |
| `install` | 注册计划任务（SYSTEM 身份 / 开机延迟 30s / 失败每分钟重启） |
| `uninstall` | 删除该计划任务 |
| `taskxml` | 打印计划任务 XML，供无法自动注册时手动导入 |
| `decode <hex>` | 解密一个门户 `encode()` 密文，显示盐与明文（用于核对算法） |
| `version` / `help` | 版本 / 帮助 |

通用参数：`-dir <工作目录>`、`-user <账号>`、`-portal <门户地址>`。

## 4. 工作原理

### 4.1 判定：多探针投票 + 门户接口兜底

每轮分两步，**先探针、后门户接口**：

**第 1 步 · 多探针并发投票**

并发请求 `probes` 里配置的多个独立厂商端点（默认 6 个：4 家厂商的 HTTP + 2 家的 DoH）。
一个探针被判为"通"的条件是二者之一：

1. HTTP **200**，且 `expect` 为空或正文包含 `expect`（忽略大小写）；
2. HTTP **3xx**（301/302/303/307/308），且 `Location` **不指向门户**。

第 2 条很关键：实测必应会跳 `http://cn.bing.com/...`、腾讯云会跳 `https://cloud.tencent.com/...`，
若只认 200，正常跳转也会被判为不通，导致**每轮都误重登**。
而门户劫持的跳转目标是门户自身，所以用「Location 是否指向门户」精确区分。

成功数 ≥ `probe_threshold`（默认 4）即判为**网络正常**，本轮结束，**不再请求门户接口**。

**第 2 步 · 探针不达标时，查门户认证态**

```
POST /api/ip.php
  ├─ logined == 0  → 已掉线，立即重登
  └─ logined == 1  → 认证态尚在但外网不通
                     连续 net_fail_threshold 轮（默认 2）才判为"会话僵死"并重登
                     未达阈值只记录观察，避免因瞬时抖动白白挤掉另一台设备
```

之所以把多探针放在前面：单一探针极易被"目标站点本身在本网络不可达"误伤
（实测 `www.msftconnecttest.com` 在校园网连不通），
一旦误判就会反复重登，反而不断挤掉使用者其它设备。

探针混用 HTTP 与 HTTPS 两种协议：**HTTP 探针能被门户劫持"看见"**，
用于判定认证态是否失效；**HTTPS/DoH 探针门户劫持不了**，用于判定链路本身是否通。

### 4.2 重登时序

```
POST /api/ip.php        建立会话，取得会话 Cookie
POST /api/login.php     user & pass & authmode & pool & isp_id & pxyacct
POST /api/ack_auth.php  认证确认
POST /api/stat.php      状态确认，期望 msg 为「认证成功！」
（复核）多探针 + 新建会话再探一次 ip.php
```

### 4.3 仅使用 `logined`，不看 `sessionlogined`

`acct` 与 `sessionlogined` 反映的是"当前 PHP 会话绑定的账号"，每个新会话都是空/0
（本机独占 IP 时也如此）。用它们判定会误判，因此只用 `logined`。

### 4.4 抖动抑制

- 多探针投票本身就抗单点抖动（4 个里坏 1 个不影响结论）
- `logined=1` 但探针不达标时，需连续 `net_fail_threshold` 轮（默认 2）才重登
- `logined=0` 则**立即**重登 —— 这个信号无歧义，不需要等

### 4.5 前置守卫

只有当本机存在 `expected_ip_prefix`（默认 `10.112.`）网段的地址时才尝试认证，
避免笔记本接到别的网络时向校园门户发起无意义的登录。

### 4.6 退避与抖动

正常间隔 30s；连续失败按 2 倍退避至 `max_backoff_seconds` 上限；
每次休眠附加 0–7s 随机抖动。连续正常时**不重复写日志**，只在状态变化时记录。

## 5. 配置文件

`config.json`（示例见 `config.example.json`）：

| 键 | 默认 | 说明 |
|---|---|---|
| `portal` | `http://10.20.33.101` | 门户地址 |
| `user` | — | 账号 |
| `password` | — | **明文**。文件已 gitignore |
| `authmode` / `pool` / `isp_id` / `pxyacct` | `0 / "" / 0 / ""` | 与抓包一致；`pool`/`pxyacct` 用于多运营商代拨选路，本部署为空 |
| `expected_ip_prefix` | `10.112.` | 前置守卫：本机须有此网段地址 |
| `interval_seconds` | `30` | 正常轮询间隔 |
| `max_backoff_seconds` | `300` | 退避上限 |
| `timeout_seconds` | `10` | 单次请求超时 |
| `probes` | 6 个（见下） | 探针列表，每项 `{name, url, expect}`。并发请求，多探针投票 |
| `probe_threshold` | `4` | 至少多少个探针通过才判为网络正常 |
| `probe_timeout_seconds` | `8` | 单个探针的超时 |
| `net_fail_threshold` | `2` | 探针不达标但 `logined=1` 时，连续多少轮才判为会话僵死并重登 |
| `log_max_bytes` | `5242880` | 日志超过则轮转为 `agent.log.old` |

默认探针混用 **HTTP 与 HTTPS 两种协议**，各自承担不同职责：

| 探针 | 协议 | 地址 | 期望正文 |
|---|---|---|---|
| 百度 | HTTP | `http://www.baidu.com/robots.txt` | `Baiduspider` |
| 必应 | HTTP | `http://www.bing.com/robots.txt` | `msnbot` |
| 阿里云 | HTTP | `http://mirrors.aliyun.com/robots.txt` | `User-agent` |
| 腾讯云 | HTTP | `http://cloud.tencent.com/robots.txt` | `tencent` |
| 阿里DoH | HTTPS | `https://dns.alidns.com/resolve?name=www.baidu.com&type=A` | `"Status":0` |
| 腾讯DoH | HTTPS | `https://doh.pub/dns-query?name=www.baidu.com&type=A` | `"Status":0` |

- **HTTP 探针**：门户能劫持 HTTP，所以这类探针能"看见"认证失效（被劫持或返回门户页面即判不通）；
- **HTTPS/DoH 探针**：门户无法劫持 HTTPS，用于判定"链路本身是否通"，并顺带验证 DNS 可用性。

> **两个实测踩过的坑**
>
> 1. **不要用 `www.msftconnecttest.com`**：它在部分校园网完全连不通（连测 6 次全 000），
>    会导致误判掉线并反复重登 —— 反而不断挤掉使用者的其它设备。
> 2. **阿里云 DoH 有两套接口，参数不同**（很容易传错）：
>    - `https://dns.alidns.com/dns-query` —— RFC 8484 **wire 格式**，要 `?dns=<base64url 二进制报文>`，
>      并带 `accept: application/dns-message`；传 `name=` 会被拒（"no 'dns' query parameter found"）。
>    - `https://dns.alidns.com/resolve` —— **JSON 风格**，接受 `?name=&type=`，
>      需带 `accept: application/dns-json`。本仓库用的是这个。
>    - IP 形式 `https://223.5.5.5/resolve` 也可用（阿里证书 SAN 中带该 IP，故校验能过）。

## 6. 排错

```powershell
# 计划任务状态
schtasks /Query /TN CampusPortalAgent /V /FO LIST

# 日志（滚动）。注意必须指定 UTF8，否则 PowerShell 5.1 按 ANSI 读会显示乱码
Get-Content C:\ProgramData\CampusPortalAgent\agent.log -Encoding UTF8 -Tail 50

# 只读诊断
.\portalagent.exe status
```

| 现象 | 可能原因 |
|---|---|
| 一直输出"未检测到 10.112. 网段地址" | 网段变了，改 `expected_ip_prefix`；或不在校园网内 |
| 日志显示 `缺少 user 或 password` | `config.json` 未填完 |
| 日志显示 `login.php ret=...` | 账号被限制、密码已改、或并发数上限触顶。按 `msg` 判断 |
| 认证成功但外网仍不通 | 检查是否有其他策略（限速/封锁）或本机 DNS |
| 日志反复出现"检测到掉线 → 已重新认证成功" | 某个探针目标在本网络不可达（用 `status` 看哪个探针挂了），或 `probe_threshold` 设得过高 |
| 任务未运行 | 确认以管理员安装；可用 `schtasks /Run /TN CampusPortalAgent` 手动触发 |
| `build.cmd` 报 `'xxx' is not recognized` | 文件被编辑器改成了 LF 换行或含非 ASCII 字符。批处理要求 **CRLF + 纯 ASCII**（仓库已用 `.gitattributes` 锁定 `*.cmd` 为 CRLF） |

## 7. 卸载

```powershell
.\portalagent.exe uninstall
Remove-Item -Recurse -Force C:\ProgramData\CampusPortalAgent
```

## 8. 已知局限与注意事项

1. **实测挤兑策略：挤出"存活最久"的那台设备。**
   含义有两面：
   - **对本工具有利**：每次重登后本机都是**最新会话**，因而排在淘汰队列末尾，
     实际上获得了稳定占用名额的效果。
   - **但每次重登都会挤掉另一台设备**。如果手机/平板也在自动重连
     （系统级"需要认证"自动弹门户 + 勾了记住密码），就会形成乒乓：
     `平板重连 → 挤掉本机 → agent 重登 → 挤掉平板 → 平板重连 → …`
     每一轮两边都断一次，体验比不用还差。
   → 因此**只在一台设备上跑本 agent**，并关闭其它设备对该 SSID 的自动认证。
2. **不要加"定期主动刷新会话"。** 既然淘汰的是最久的那台，
   主动刷新会平白挤掉别的设备，而对保持本机在线没有任何额外收益。
   当前"被踢才重登"的策略是正确的。
3. **轮询要克制。** 默认 30s + 退避已足够；请不要改成秒级，那会退化成对门户的轮询压测。
4. **策略层面：** 本工具不突破运营商策略，但它确实在主动争夺并发名额。
5. **RDP 侧建议：** 不要把 3389 直接暴露在校园网。走 ZeroTier 等隧道更安全；同时记得
   关闭睡眠/休眠，否则认证在不在都没意义。
6. `expected_ip_prefix` 是本部署实测值（有线 `10.112.0.0/16`），换环境需更新。

## 9. 安全声明

- 仅用于**使用者本人账号、本人设备**的自动重登保活
- 不用于、也不应被用于获取他人凭据
- 不绕过运营方的并发策略，也不试图规避任何审计
- 本工具是针对特定门户**客户端行为**的复刻，不包含任何针对服务端的探测或攻击手段
