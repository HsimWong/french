# 接入指南 · 把 Go 后端网页挂到固定入口

给「跑在自己电脑上、靠 Cloudflare 临时隧道暴露」的 Go 程序一个**不会变的公网地址**。

适用场景：程序在本地跑，用 `cloudflared tunnel --url` 拿到一个 `xxx.trycloudflare.com`
临时域名，但每次重启域名都会变。接入本网关后，用户只需要记住网关这个固定链接。

```
浏览器 ──> 网关（固定链接） ──> 程序当前隧道域名
                            └──> 另一个程序的隧道域名
```

> 如果你的程序已经有自己的域名 + `cloudflared named tunnel`，域名本来就是固定的，
> **不需要接这个网关**。它解决的是「临时域名会变」和「多个本地程序要统一入口」。

---

## 一、两种工作模式（先搞清楚你接的是哪种）

网关由 `GW_MODE` 决定行为，**默认 `redirect`**。你的程序需要做的事情不同：

| | `redirect`（默认，门户跳转） | `proxy`（反向代理） |
|---|---|---|
| 用户点 `/myapp/` | **302** 跳到 `https://xxx.trycloudflare.com/` | 网关替你转发，地址栏保持网关域名 |
| 地址栏 | 变成隧道域名，之后直连隧道 | 始终是网关域名 |
| 你的程序要不要改前端路径 | **不用改**（浏览器认为自己在根路径） | **必须改**（见第五节） |
| 网关挂了 | 已打开的页面不受影响 | 立刻 502 |
| 隧道换域名 | 用户下次点固定入口自动跳新的 | 网关 5 秒内改指新域名 |

用 302 不用 301 是刻意的：301 会被浏览器长期缓存，隧道一换域名用户就被钉在死链上。

两种模式下**上报接口完全一样**，所以代码只写一遍，模式可以随时切换。

---

## 二、三步接入

### 步骤 1：拿到三样东西

| 名字 | 从哪来 | 例子 |
|---|---|---|
| `GATEWAY_URL` | 网关的固定链接，**必须带 `?sharecode=`**（不带会 403） | `https://wbapp-xxx.app.workbuddy.host/?sharecode=QPFVA7KHN2` |
| `GATEWAY_TOKEN` | 网关的 `.env` 里的 `GW_TOKEN` | `5f59da26...` |
| `PREFIX` | 自己定，URL 第一段 | `finance`、`notes`、`my-app` |

`PREFIX` 规则：`^[a-z0-9][a-z0-9-]{0,30}$`（小写字母/数字/连字符，字母或数字开头，≤31 字符）。
`api`、`_gw`、`favicon.ico` 被网关占用，不能用。

### 步骤 2：上报一次，确认通

```bash
curl -X POST "https://<网关>/api/register?sharecode=XXXX" \
  -H "Content-Type: application/json" \
  -H "X-GW-Token: <GW_TOKEN>" \
  -d '{"name":"我的程序","prefix":"myapp","target":"https://xxx.trycloudflare.com"}'
```

返回：

```json
{"ok": true, "prefix": "myapp", "path": "/myapp/"}
```

然后浏览器打开 `<网关>/myapp/` 就能看到你的程序了（`redirect` 模式会跳过去）。

> 注意地址拼法：网关地址里的查询串要**跟在 `/api/register` 后面**，
> 不能插在路径中间（`https://host/?sharecode=X/api/register` 是错的，会 404）。

### 步骤 3：让程序自己持续上报

手动 curl 只够验证。真正要用，上报必须**由服务进程自己做**，原因有两个：

1. 启动脚本的前台循环一结束（关终端、断 SSH、脚本被杀），入口就再没人更新；
2. 网关侧可能被回收重启、注册表被清空，只有周期性心跳能让它自己长回来。

代码见第四节，拷两个文件即可，零第三方依赖。

---

## 三、接口契约

所有接口都在网关根路径下，返回 JSON。

### POST /api/register —— 注册/更新（幂等）

**鉴权**：`X-GW-Token: <GW_TOKEN>`（也支持 `?token=` 或 Cookie `gw_token`）。
**请求体**（`Content-Type: application/json`）：

```json
{
  "name":   "我的程序",
  "prefix": "myapp",
  "target": "https://xxx.trycloudflare.com"
}
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `prefix` | 是 | URL 第一段，规则见上。会先 `trim` + **转小写**再校验，所以传 `MyApp` 会成功并注册成 `/myapp/`（不会报错，但别依赖这点） |
| `target` | 是 | 当前公网地址。只允许 `https`；默认**禁止**内网/环回地址（防 SSRF），本地联调设 `GW_ALLOW_INSECURE_TARGET=1` 才放开 `http` 与内网。查询串和锚点会被丢弃 |
| `name` | 否 | 显示名，空则等于 `prefix` |

**成功** `200`：

```json
{"ok": true, "prefix": "myapp", "path": "/myapp/"}
```

**失败**（下面是线上实测返回的真实文案）：

| 状态码 | `error` 内容 | 处理 |
|---|---|---|
| `401` | `token 不正确` | 检查 `GATEWAY_TOKEN` 是否与网关 `.env` 的 `GW_TOKEN` 一致 |
| `400` | `该前缀为网关保留，请换一个` | `api`/`_gw`/`favicon.ico` 不能用 |
| `400` | `前缀只能包含小写字母、数字和连字符，且以字母/数字开头，最长 31 字符` | 改前缀 |
| `400` | `目标地址必须是 https` | `target` 用 `https://`，或本地联调时开 `GW_ALLOW_INSECURE_TARGET=1` |
| `400` | `不允许指向内网或环回地址` | 同上 |
| `405` | `只支持 POST` | 必须 POST |
| `500` | `写入注册表失败` | 重试；持续失败说明网关磁盘/权限有问题 |

**幂等性**：同一个 `prefix` 反复上报只是更新 `target` 和时间戳，重复发没有副作用，也不会产生多条记录。所以可以放心地无脑每 60 秒报一次。

### GET /api/registry —— 查看注册表

需要鉴权。返回 `{"ok":true,"entries":[{...}]}`，按 `prefix` 排序：

```json
{
  "ok": true,
  "entries": [
    {
      "name": "家庭财务系统",
      "prefix": "finance",
      "target": "https://xxx.trycloudflare.com",
      "updated_at": "2026-11-14T01:30:30+08:00",
      "healthy": true,
      "probed_at": "2026-11-14T01:30:35+08:00",
      "probe_note": "在线（需要口令）",
      "latency_ms": 1554
    }
  ]
}
```

排障时很有用：`target` 是网关**当前**指向的地址，`updated_at` 是最后一次上报时间
（超过两三个心跳周期没动 = 上报停了），`healthy` 是网关探活结果。

探活规则：`2xx`/`3xx`/`401`/`403` 都算在线（401/403 说明服务在跑、只是要口令），
`5xx` 与连接失败才算不健康。所以**你的程序加了访问口令不影响被判定为健康**。

### GET /api/delete?prefix=myapp —— 移除

需要鉴权。带 `Accept: application/json` 返回 JSON，否则 302 回管理页。
移除后程序下次上报会自动重新出现，所以它是「踢掉一个不想要的记录」而不是「拉黑」。

### GET /_gw/health —— 健康检查

无需鉴权。返回 `{"ok":true,"entries":3,"uptime":"1h2m3s"}`。
`uptime` 会归零说明网关进程被重启过（沙箱回收），此时注册表是空的，等各程序心跳补回来。

### 鉴权优先级

`X-GW-Token` 头 → `?token=` 查询参数 → Cookie `gw_token`，任一匹配即可，常量时间比较。
网关 `GW_TOKEN` 为空时**不鉴权**（只适合本地联调），公网部署前必须设置。

---

## 四、Go 接入模板

网关仓库里有 `gwreport/` 包，**两个文件、零第三方依赖、go 1.21**，直接拷到你项目里：

```
gateway/gwreport/gwreport.go    ← 上报器本体
gateway/gwreport/tunnellog.go   ← 从 cloudflared 日志取当前域名
```

### 4.1 最小接法

```go
import (
    "context"
    "os"

    "yourmodule/gwreport" // 拷过来的包
)

func main() {
    // ... 起你的 HTTP 服务 ...

    rep := gwreport.New(gwreport.Config{
        Gateway: os.Getenv("GATEWAY_URL"),   // https://host/?sharecode=XXXX
        Token:   os.Getenv("GATEWAY_TOKEN"),
        Prefix:  os.Getenv("GATEWAY_PREFIX"), // 如 myapp
        Name:    "我的程序",
        Current: func() string {
            return gwreport.TunnelURLFromLog("logs/tunnel.log")
        },
    })
    rep.Start(context.Background()) // 非阻塞；没配 GATEWAY_URL 时是空操作
}
```

`Start` 干三件事：

- **立刻上报一次**（别让固定入口空等）；
- 每 `ScanEvery`（默认 5s）检查 `Current()` 返回值，**变了才发请求**；
- 每 `Heartbeat`（默认 60s）**无条件重报**，负责让网关重启后的注册表自愈。

失败后按 `RetryAfter`（默认 30s）退避，避免网关挂着时一直撞门。

`Current` 返回 `""` 表示「隧道还没起来」，此时静默跳过，不算错误。

### 4.2 域名从哪来

**A. quick tunnel（域名会变）—— 读 cloudflared 日志**

```go
Current: func() string { return gwreport.TunnelURLFromLog("logs/tunnel.log") }
```

**必须取最后一个域名**。cloudflared 重连会往日志追加新域名，旧域名随即被 Cloudflare 回收；
取第一个就会把网关指到死链上，用户看到的是 **530**。`TunnelURLFromLog` 已经处理了这点。

cloudflared 启动时记得清掉终端里的代理变量，否则会连边缘 7844 超时：

```bash
env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
    -u http_proxy -u https_proxy -u all_proxy \
    cloudflared tunnel --url http://127.0.0.1:8080 --protocol http2 --no-autoupdate
```

**B. named tunnel / 自有域名（域名固定）**

```go
Current: func() string { return "https://app.example.com" }
```

每次都报同样的地址也没关系，网关是幂等的。

**C. 环境变量（手动部署）**

```go
Current: func() string { return os.Getenv("PUBLIC_URL") }
```

### 4.3 配置建议

把这几项放进 `.env`，与程序其他配置一起读：

```dotenv
# 网关固定入口（带 sharecode，否则沙箱会 403）
GATEWAY_URL=https://wbapp-xxx.app.workbuddy.host/?sharecode=QPFVA7KHN2
GATEWAY_TOKEN=<GW_TOKEN>
GATEWAY_PREFIX=myapp
# 可选
GATEWAY_INTERVAL=60s      # → Config.Heartbeat
GATEWAY_SCAN=5s           # → Config.ScanEvery，域名变化检测
GATEWAY_TUNNEL_LOG=logs/tunnel.log  # → Current 里传给 TunnelURLFromLog
```

对应到 `Config`：

| `.env` | `Config` 字段 | 默认 |
|---|---|---|
| `GATEWAY_URL` | `Gateway` | 空 = 功能关闭 |
| `GATEWAY_TOKEN` | `Token` | 空 = 不带鉴权头 |
| `GATEWAY_PREFIX` | `Prefix` | 必填 |
| `GATEWAY_INTERVAL` | `Heartbeat` | `60s` |
| `GATEWAY_SCAN` | `ScanEvery` | `5s` |
| `GATEWAY_TUNNEL_LOG` | 喂给 `Current` | `logs/tunnel.log` |

**判断开关**：`Gateway` 为空时 `New` 返回 `nil`，`Start`/`Register` 都是安全的空操作 ——
所以没有网关的环境（同事本机、CI）不需要改代码，配了才生效。

### 4.4 一个可运行的最小例子

不接隧道也能验证上报通不通：先手工上报当前进程监听的本机地址（需网关开
`GW_ALLOW_INSECURE_TARGET=1`），看 `GET /api/registry` 里有没有出现自己的记录。

```go
package main

import (
    "context"
    "fmt"
    "net/http"
    "time"

    "yourmodule/gwreport"
)

func main() {
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintln(w, "hello from myapp")
    })
    go http.ListenAndServe(":8080", nil)

    rep := gwreport.New(gwreport.Config{
        Gateway:   "http://127.0.0.1:8091", // 本地网关
        Token:     "devtoken",
        Prefix:    "myapp",
        Name:      "接入测试",
        Heartbeat: 30 * time.Second,
        Current:   func() string { return "http://127.0.0.1:8080" },
    })
    rep.Start(context.Background())
    select {}
}
```

网关侧 `GW_ALLOW_INSECURE_TARGET=1 GW_TOKEN=devtoken ./gateway.exe -addr 127.0.0.1:8091`，
然后 `curl 'http://127.0.0.1:8091/api/registry?token=devtoken'` 就能看到条目。

---

## 五、前端硬要求：必须用相对路径

**只有 `proxy` 模式需要这一节。`redirect` 模式下浏览器直接访问隧道根路径，绝对路径也能用。**

在 `proxy` 模式下，你的程序被挂在 `<网关>/myapp/` 下。页面里写 `/api/meta`，浏览器会去请求
`<网关>/api/meta` —— 打到网关根目录，**404**。

```javascript
fetch('/api/meta')            // ✗ 打到网关根目录
fetch('api/meta')             // ✓ 相对当前路径 /myapp/
```

```html
<link rel="stylesheet" href="/css/style.css">   <!-- ✗ -->
<link rel="stylesheet" href="css/style.css">    <!-- ✓ -->
```

对于挂在 `/myapp/` 下的页面，相对路径会正确解析成 `/myapp/api/meta`。
前提是 `GET /myapp` 必须 301 到 `/myapp/`（网关已处理），否则相对路径基准会错。

检查你项目里还有多少绝对路径：

```bash
grep -nE '(href|src|action)="/' *.html
grep -noE "['\"\`]/(api|css|js)/[^'\"\`]*" js/*.js
```

**网关改不了的地方**：HTML 里的 `href/src/action/poster` 属性网关会兜底重写（只处理**双引号**且以单个 `/`
开头的值），但**内联 `<script>` 里的绝对 `fetch('/api/x')` 改不了**（盲改 HTML 里的所有 JS 字符串太危险）。
所以这项必须由程序自己保证。

另外 `proxy` 模式会给上游加这些头，需要拼绝对链接时可以用：

| 头 | 值 |
|---|---|
| `X-Forwarded-Prefix` | `/myapp` |
| `X-Forwarded-Host` / `X-Forwarded-Proto` | 原始请求的域名与协议 |

上游收到的 `Host` 是它自己的隧道域名，路径已经 strip 掉前缀——也就是**你的程序以为自己在根路径**。

---

## 六、排错表

| 现象 | 含义 | 处理 |
|---|---|---|
| `/myapp/` 返回 **404** | 注册表里没这个 prefix | 程序没起来，或上报被拒（`401` token 错）。查 `GET /api/registry` |
| `/myapp/` 返回 **530** | 网关指的隧道域名已死（隧道刚重启换了域名，注册表还没更新） | 正常情况下 ≤2 秒自愈；一直 530 说明上报挂了或停了 |
| `/myapp/` 返回 **502**（`proxy` 模式） | 网关连不上上游 | 隧道断了，或本机服务没起 |
| **401**（来自网关） | 上报/管理接口没带 token | 加 `X-GW-Token` |
| **401**（来自你的程序） | 正常，通常是你的口令保护 | 输口令即可 |
| **403** | WorkBuddy 分享链接缺 `sharecode` | `GATEWAY_URL` 里必须带完整查询串 |
| 页面能开但点不动 | Cookie `Path` 与访问路径不匹配（`proxy` 模式） | 从前缀地址进入，不要用别的路径拼 |
| 上报日志里 `HTTP 400` | `prefix` 或 `target` 不合规 | 看返回的 `error` 字段 |
| 上报日志里 `HTTP 401` | `GATEWAY_TOKEN` 与网关 `GW_TOKEN` 不一致 | 改成一致后重启程序 |
| 上报一直不发 | `Current()` 返回空 | 隧道没起来，或日志路径写错（相对路径以程序工作目录为准） |

---

## 七、安全

- **`GW_TOKEN` 是上报凭据**。拿到它的人可以往网关注册任意地址，把你的固定入口指到别处。
  不要写进前端、不要提交到公开仓库；泄露了就改网关 `.env` 的 `GW_TOKEN` 和所有程序的
  `GATEWAY_TOKEN`（两处必须一致），然后重新发布网关。
- **`sharecode` 是访问凭据**，等价于门牌口令，同样别外传。
- **网关只接受 `https` 目标，且默认拒绝内网/环回地址**，防止 token 泄漏后被拿来做 SSRF。
  本地联调才设 `GW_ALLOW_INSECURE_TARGET=1`，且不要带进线上配置。
- **鉴权是共享口令，不是多租户**。所有程序用同一个 token，任一程序都能改别人的记录。
  自家几个程序够用；如果要给别人用，需要换成每个 prefix 一把密钥。

---

## 八、接入 checklist

- [ ] 拿到 `GATEWAY_URL`（带 `?sharecode=`）、`GATEWAY_TOKEN`、定好 `PREFIX`
- [ ] `curl` 手动上报一次，`GET /api/registry` 能看到自己的记录
- [ ] 拷 `gwreport/` 两个文件进项目，`go build` 通过（零第三方依赖）
- [ ] `Current()` 返回的路径正确；用 cloudflared 日志时**取最后一个域名**
- [ ] 上报挂在服务进程里，不是启动脚本里
- [ ] `proxy` 模式：前端全部改相对路径，`grep` 确认无残留
- [ ] 隧道重启演练一次：杀掉 cloudflared，确认网关在几秒内改指新域名
- [ ] 浏览器从固定入口打开，功能完整（含上传、登录、静态资源）

---

## 相关文件

- 网关源码：`C:\Users\WangXin\WorkBuddy\gateway`（发布副本 `gateway-deploy/`，改完跑 `bash sync.sh`）
- 上报包：`gateway/gwreport/`（含测试，`go test ./gwreport`）
- 参考实现：`C:\Users\WangXin\WorkBuddy\财务管理\src\backend\reporter.go`（家庭财务系统的上报器，含配置解析与日志策略）
