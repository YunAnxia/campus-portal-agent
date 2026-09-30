# portalagent —— 校园网门户自动重登保活代理

针对 `http://10.20.33.101`（Panabit RAAS 认证计费系统）的门户认证自动重登工具。
**用途：让本机在账号被其他设备挤下线后自动重新认证，从而保持可远程访问（RDP）。**

Go 1.23 编写，**纯标准库、零外部依赖**（DPAPI 直接 syscall 调用 `crypt32.dll`）。
产出单一静态 `portalagent.exe`。

相关文档：[ANALYSIS.md](ANALYSIS.md) —— 门户认证流程与设计依据的简述。

> **本仓库为私有仓库。** 其中不含账号、口令或任何可重放的凭据；
> `config.json`、`credential.dpapi`、日志与二进制均已通过 `.gitignore` 排除。

---

## 0. 先读这一节：先试更省事的办法

在部署本工具之前，请先排除以下三种情况 —— 它们都能让脚本变得不必要：

1. **本机是否有多余网卡在占名额？**
   抓包与实测期间本机同时存在 `以太网 10.112.196.67` 与 `WLAN 10.122.18.65` 两块会拿 IP 的网卡。
   如果校园网按 MAC 计数并发，**一台机器就可能占掉两个名额**。
   → 用以太网时禁用 WLAN，很可能直接解决问题。

2. **是不是手机/平板在抢名额？**
   若踢人的来源是自己的另一台设备，减少竞争设备比写脚本更有效。

3. **是否只是空闲超时？**
   若如此，调策略或降低探测间隔即可，不需要完整重登。

另外强烈建议先问网络中心有没有**正式途径**（设备 MAC 绑定、提高并发数、专用远程访问通道）。

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

# 2) 从示例生成配置，填入你的账号
Copy-Item .\config.example.json .\config.json
notepad .\config.json      # 至少改 user；若网段不同，同步改 expected_ip_prefix

# 3) 建议放到固定位置，避免被移动后计划任务失效
New-Item -ItemType Directory -Force C:\ProgramData\CampusPortalAgent | Out-Null
Copy-Item .\portalagent.exe, .\config.json, .\set-cred.cmd C:\ProgramData\CampusPortalAgent\

# 4) 设置凭据（密码不回显，用 DPAPI 加密后写入 credential.dpapi）
cd C:\ProgramData\CampusPortalAgent
.\portalagent.exe set-cred
#   也可以直接双击 set-cred.cmd

# 5) 验证（只读，不做任何认证动作，可随时安全运行）
.\portalagent.exe status

# 6) 单次试跑（当前已认证时应输出 ok 且不发起登录）
.\portalagent.exe once

# 7) 注册开机自启的计划任务（需管理员）
.\portalagent.exe install
```

安装后在「任务计划程序」中找到 `CampusPortalAgent`，右键 →「运行」验证一次。

## 3. 命令

| 命令 | 说明 |
|---|---|
| `status` | **只读**。打印本机校园网地址、`ip.php` 返回、外网连通性、判定结果。不发起认证，可随时运行 |
| `once` | 执行一个周期：检测，必要时重登，然后退出。用于试跑 |
| `run` | 常驻循环（默认命令），供计划任务使用 |
| `set-cred` | 交互设置账号与密码，密码以 DPAPI 加密写入 `credential.dpapi` |
| `install` | 注册计划任务（SYSTEM 身份 / 开机延迟 30s / 失败每分钟重启） |
| `uninstall` | 删除该计划任务 |
| `taskxml` | 打印计划任务 XML，供无法自动注册时手动导入 |
| `decode <hex>` | **仅取证用**。解密一个门户 `encode()` 密文，显示盐与明文（用于复核抓包） |
| `version` / `help` | 版本 / 帮助 |

通用参数：`-dir <工作目录>`、`-user <账号>`、`-portal <门户地址>`。

## 4. 工作原理

### 4.1 判定

每轮先做两个检查，**两者都通过**才算正常：

1. `POST /api/ip.php` → `data.logined == 1`
2. 真实连通性检查：GET `connectivity_url`，要求 HTTP 200 且内容包含 `connectivity_expect`

第 2 步很重要：光看 `logined` 不够，某些状态下接口会返回成功但实际流量被丢弃。
检查时**不跟随重定向**，因此门户劫持会表现为 3xx 或内容不符，被判为"未连通"。

### 4.2 重登时序（严格复刻抓包）

```
POST /api/ip.php        建立 PHP 会话，取得 RAASSESSID
POST /api/login.php     user & pass & authmode & pool & isp_id & pxyacct
POST /api/ack_auth.php  AFF_ACK_AUTH 认证确认
POST /api/stat.php      状态确认，期望 msg == "认证成功！"
POST /api/ip.php        复核（新会话）
```

字段顺序与抓包一致；空 body 的 POST 会发出 `Content-Length: 0`，与真实流量形态相同。

### 4.3 仅使用 `logined`，刻意不看 `sessionlogined`

`acct` 与 `sessionlogined` 反映的是"当前 PHP 会话绑定的账号"，每个新会话都是空/0
（本机独占 IP 时也如此）。用它们判定会误判，因此只用 `logined`。

### 4.4 前置守卫

只有当本机存在 `expected_ip_prefix`（默认 `10.112.`）网段的地址时才尝试认证。
避免笔记本插到别的网络（家里/热点）时向校园门户发起无意义的登录。

> 如果你的 IP 网段变了，请同步修改 `config.json` 的 `expected_ip_prefix`。

### 4.5 退避与抖动

正常间隔 30s；连续失败按 2 倍退避至 300s 上限；每次休眠附加 0–7s 随机抖动，
避免多设备同步轮询。连续正常时**不重复写日志**，只在状态变化时记录。

## 5. 凭据处理

- 密码以 **Windows DPAPI** 加密存储（默认 `machine_scope: true`，绑定本机）
- 选择本机范围而非当前用户范围，是为了让**以 SYSTEM 身份运行的计划任务**能够解密，
  同时避免在计划任务里保存 Windows 账户密码
- `pass` **每次登录现算**：4 位随机盐 + 明文 → AES-128-ECB/ZeroPadding → hex
  - 盐的字符集与取值范围完全复刻前端（`A-Za-z0-9+/=` 的下标 0..60，**含 `8` 不含 `9`**）
  - 这样每次登录的密文都不同，不会把一个静态可重放凭证反复摊进日志
  - 算法一致性由单元测试保证（见下）
- 明文口令以 `[]byte` 传递，用后立即清零；但 Go 的字符串不可清零，
  且 Go 运行时可能已产生副本 —— **这是语言层面的局限，构建时已尽量减少明文驻留**

### 单元测试覆盖

```
TestEncryptMatchesIndependentVector  与独立 .NET AES 实现算出的向量逐字节比对，
                                     证明 AES-128-ECB + ZeroPadding 语义正确
                                     （加密路径已拆为 encryptPortalBlob，可用固定输入测试）
TestZeroPaddingDoesNotAddFullBlock   恰好按块对齐时不追加整块 —— 与 PKCS#7 的关键区别
TestEncodeRoundTrip                  多种长度口令的编码→解码往返一致，
                                     并校验盐字符落在前端取值范围内
TestEncodeEmptyRejected              空口令必须被拒绝
TestEncodeDecodeAreInverse           编码/解码严格互逆
```

> 关于正确性验证方式的说明：加密算法的正确性**不依赖本包自身的往返**（那会构成循环论证），
> 而是与一份由独立实现（.NET `System.Security.Cryptography.Aes`）预先算出的向量比对。
> 该向量使用固定输入、不含任何真实凭据。

## 6. 配置项

| 键 | 默认 | 说明 |
|---|---|---|
| `portal` | `http://10.20.33.101` | 门户地址 |
| `user` | — | 账号 |
| `authmode` / `pool` / `isp_id` / `pxyacct` | `0 / "" / 0 / ""` | 与抓包一致；`pool`/`pxyacct` 是代拨账号与线路选择，本部署为空 |
| `expected_ip_prefix` | `10.112.` | 前置守卫：本机须有此网段地址 |
| `interval_seconds` | `30` | 正常轮询间隔 |
| `max_backoff_seconds` | `300` | 退避上限 |
| `timeout_seconds` | `10` | 单次请求超时 |
| `connectivity_url` / `connectivity_expect` | msftconnecttest | 真实连通性检查 |
| `machine_scope` | `true` | DPAPI 机器范围（SYSTEM 任务可解密） |
| `log_max_bytes` | `5242880` | 日志超过则轮转为 `agent.log.old` |

## 7. 排错

```powershell
# 计划任务状态
schtasks /Query /TN CampusPortalAgent /V /FO LIST

# 日志（滚动）
Get-Content C:\ProgramData\CampusPortalAgent\agent.log -Tail 50

# 只读诊断
.\portalagent.exe status
```

| 现象 | 可能原因 |
|---|---|
| 一直输出"未检测到 10.112. 网段地址" | 网段变了，改 `expected_ip_prefix`；或不在校园网内 |
| `读取凭据失败 / CryptUnprotectData 失败` | 凭据由其他机器或其他用户加密。用**将来的运行身份**重新执行 `set-cred` |
| 日志显示 `login.php ret=...` | 账号被限制、密码已改、或并发数上限触顶。按 `msg` 判断 |
| 认证成功但外网仍不通 | 检查是否有其他策略（限速/封锁）或本机 DNS |
| 任务未运行 | 确认以管理员安装；服务方式可用 `schtasks /Run /TN CampusPortalAgent` 手动触发 |
| `build.cmd` 报 `'xxx' is not recognized` | 文件被编辑器改成了 LF 换行或含非 ASCII 字符。批处理要求 **CRLF + 纯 ASCII**；可改用 `go build` 直接编译 |

## 8. 卸载

```powershell
.\portalagent.exe uninstall
Remove-Item -Recurse -Force C:\ProgramData\CampusPortalAgent
```

## 9. 已知局限与注意事项

1. **实测挤兑策略：挤出"存活最久"的那台设备**（2026-09-30 实测确认）。
   含义有两面：
   - **对本工具有利**：每次重登后 PC 都是**最新会话**，因而排在淘汰队列末尾，
     实际上获得了稳定占用名额的效果 —— PC 不会再"越挤越靠前"。
   - **但每次重登都会挤掉另一台设备**。如果手机/平板也在自动重连
     （系统级"需要认证"自动弹门户 + 勾了记住密码），就会形成乒乓：
     `平板重连 → 挤掉 PC → agent 重登 PC → 挤掉平板 → 平板重连 → …`
     每一轮两边都断一次，体验比不用还差。
   → 因此**只在 PC 上跑本 agent**，并关闭其它设备对该 SSID 的自动认证。
2. **不要给 agent 加"定期主动刷新会话"。** 既然淘汰的是最久的那台，
   主动刷新会平白挤掉别的设备，而对保持 PC 在线没有任何额外收益。
   当前"被踢才重登"的策略是正确的。
3. **不要在多台设备上同时部署。** 会形成互相踢的乒乓。
4. **日志编码坑**：`agent.log` 是 UTF-8，而 Windows PowerShell 5.1 默认按 ANSI 读取，
   直接 `Get-Content` 会显示乱码。排查时请用
   `Get-Content .\agent.log -Encoding UTF8 -Tail 50`。
5. **轮询要克制。** 默认 30s + 退避已足够；请不要改成秒级，那会退化成对门户的轮询压测。
6. **策略层面：** 本工具不突破运营商策略，但它确实在主动争夺并发名额。
   请先确认学校是否有正式的设备绑定/远程访问方案。
7. **RDP 侧建议：** 不要把 3389 直接暴露在校园网。走 ZeroTier 等隧道更安全；同时记得
   关闭睡眠/休眠，否则认证在不在都没意义。
8. `expected_ip_prefix` 是本部署实测值（有线 `10.112.0.0/16`），换环境需更新。

## 10. 安全声明

本工具仅用于**使用者本人账号、本机设备**的自动重登，等价于门户自带的"记住密码"行为。
它不提供、也不应被用于获取他人凭据或绕过运营方的并发策略。
