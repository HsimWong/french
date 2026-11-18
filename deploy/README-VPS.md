# 法语学习平台 —— VPS 部署指南（Linux）

> 面向 2G 内存的小机器。整套方案的目标：**内存可控、CPU 可限、随时能拿到数据库文件**。

---

## 一、词典不进二进制（2026-11-18 修正）

早期版本把 164MB 的 `data/lexicon_seed.json` 用 `go:embed` 编进二进制，
后果是：二进制 243MB、冷编译在小 VPS 上超过 5 分钟、编译期内存占用极高，
而且源码里少了这个文件就直接编译失败。**这个设计已彻底删除。**

现在只有一种构建方式：词典是**运行时数据文件**，放在
`/home/admin/maintain/french/data/lexicon_seed.json`，由 `.env` 的
`LEXICON_SEED_FILE` 指定路径。程序启动时**流式**导入数据库（每次只解一个数组元素，
峰值内存只有几 MB），之后靠 meta 表里的指纹判断，重复启动不会重新导入。

```bash
cd /home/admin/maintain/french
go build -o bin/french .        # 约 80MB，1 核 VPS 通常 1-3 分钟
sudo make install               # 或直接覆盖 bin/french 后 systemctl restart french
```

`make` 的默认目标就是这种构建。项目里已经没有 `-tags noseed` / `-tags embed` 的区别，
旧脚本里残留这两个标签也不影响编译。

---

## 二、为什么 `/french/` 访问不到

有三个叠加的原因，逐个排查：

### 1. 服务根本没在监听（你贴的 `ss` 输出证实了这一点）

```
tcp  0  0 127.0.0.1:8081   0.0.0.0:*   LISTEN   617/finance
```

只有 finance。**8082 上什么都没有**，nginx 转过去必然 502。

### 2. 端口不一致

端口和绑定地址现在统一在 **`.env`** 里配（`ADDR` 是完整监听地址，`PORT` 是它的简写形式）：

```bash
cd /home/admin/maintain/french && cat .env     # 应有 ADDR=:8082（或 127.0.0.1:8082）
```

程序启动时会先加载 `.env`（`config.go: loadDotEnv`），`ADDR` 为空才回退到 `PORT`，
两者都没有才用默认 8080。你的 nginx 转的是 8082，所以 `.env` 里必须配成 8082。
改完 `sudo systemctl restart french`（或直接重跑二进制）即可。

### 3. 前端用了绝对路径（这个最隐蔽）

法语平台的 `web/index.html` 原本写的是：

```html
<link rel="stylesheet" href="/css/style.css">
<script type="module" src="/js/app.js"></script>
```

`web/js/*.js` 里 42 处 API 调用也全是 `/api/...` 绝对路径。

浏览器解析绝对路径时**从域名根开始**，所以页面在 `/french/` 下时，
CSS/JS/API 请求会打到 `https://你的域名/css/style.css` —— **绕过了 `/french/` 这个 location**，
落到 nginx 的默认站点，直接 404。

结果就是：HTML 页面能打开，但**样式全丢、脚本全挂、一片空白**。

对比 finance 就很明显 —— 它用的是**相对路径**（`css/style.css`），
所以 `/finance/` 子路径下工作正常。

**已修复**：法语平台的全部前端路径已改为相对路径，
后端 `scanBase()` 生成的扫描图路径也改成 `scans/...`。
现在它在根路径和任意子路径下都能正常工作。

---

## 三、完整落地步骤

### 1. 传文件

```bash
# 服务器上建目录
sudo mkdir -p /home/admin/maintain/french/data
sudo chown -R $USER /home/admin/maintain/french

# 传源码、构建产物、词典、数据库
rsync -av --exclude build --exclude .git ./ root@47.94.236.198:/home/admin/maintain/french/
```

**最关键的是数据库文件**。不传的话，服务会新建一个空库，
只含 embed 里的两门课程（语法 52 章 + 词汇 25 章 = 77 章），
**《简明法语教程》那 8 章会全部丢失**（它是走 Python 脚本导入的，不在 embed 里）。

```bash
scp french.db root@47.94.236.198:/home/admin/maintain/french/
```

传输时带上 WAL：

```bash
# 停服后再拷最干净，或者用 sqlite3 的 .backup 命令做一致性备份
sqlite3 french.db ".backup /tmp/french.db" && scp /tmp/french.db root@...:/home/admin/maintain/french/
```

### 2. 构建

```bash
cd /home/admin/maintain/french
go build -trimpath -o bin/french .   # 约 80MB，词典在 data/lexicon_seed.json
```

VPS 上直接编译即可，不需要交叉编译。首次编译（Go 缓存为空）在 1 核 / 2G 机器上
通常 1–3 分钟；如果超过 5 分钟，先确认机器没有在跑 `go test` 或内存被打满。

### 3. 配 systemd（带资源限制）

```bash
# 先把项目根的 .env.example 复制成 .env 并改好 ADDR/PORT/DB_PATH/口令
cp .env.example /home/admin/maintain/french/.env
$EDITOR /home/admin/maintain/french/.env
cp french.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now french
sudo systemctl status french
sudo journalctl -u french -f
```

> **从旧版升级（服务名 `francais` → `french`）**：本仓库现在只有一个 `french.service`。
> 如果 VPS 上还跑着旧的 `francais`，先停掉并删旧单元，再装新的；数据库、词典、`.env` 都不用动：
>
> ```bash
> sudo systemctl disable --now francais
> sudo rm -f /etc/systemd/system/francais.service
> sudo systemctl daemon-reload
> ```

单元里只有 `EnvironmentFile=/home/admin/maintain/french/.env`；下面这些**都在 `.env` 里**：

| 项 | 值 | 作用 |
|---|---|---|
| `ADDR` | `:8082`（或 `127.0.0.1:8082`） | 完整监听地址，和 nginx 对齐 |
| `PORT` | 8082 | `ADDR` 为空时的简写 |
| `ACCESS_PASSCODE` | `french2026` | 访问口令；留空 = 不启用（服务完全开放） |
| `DB_PATH` | `/home/admin/maintain/french/french.db` | 绝对路径，不受工作目录影响 |
| `MEM_LIMIT_MB` | 420 | Go 堆软上限（`debug.SetMemoryLimit`） |
| `GOMAXPROCS` | 2 | Go 并行度，真正的 CPU 阀门 |
| `GOGC` | 50 | 让 GC 更早介入 |

单元文件自身只保留 cgroup 资源限制：

| 项 | 值 | 作用 |
|---|---|---|
| `MemoryHigh` | 600M | 进程软上限（含 page cache） |
| `MemoryMax` | 800M | 进程硬上限，超了 OOM kill |
| `MemorySwapMax` | 0 | 禁用 swap |
| `CPUQuota` | 100% | 相当于 1 个核 |

### 4. 配 nginx

```nginx
map $http_upgrade $wb_conn_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 80 default_server;
    server_name _;
    location /.well-known/acme-challenge/ { root /var/www/certbot; }
    location / { return 301 https://$host$request_uri; }
}

server {
    listen 443 ssl;
    http2 on;
    server_name _;
    ssl_certificate     /etc/letsencrypt/live/47.94.236.198/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/47.94.236.198/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;
    ssl_session_cache   shared:SSL:10m;
    ssl_session_timeout 1d;
    ssl_session_tickets off;
    client_max_body_size 20m;

    # 少了这条，访问 /french（不带斜杠）会 404
    location = /french { return 301 /french/; }

    location /french/ {
        # 末尾的斜杠表示"剥掉 /french/ 前缀再转发"
        proxy_pass http://127.0.0.1:8082/;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade           $http_upgrade;
        proxy_set_header Connection        $wb_conn_upgrade;
        proxy_connect_timeout 10s;
        proxy_read_timeout    300s;
        proxy_send_timeout    300s;
    }

    location /finance/ {
        proxy_pass http://127.0.0.1:8081/;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade           $http_upgrade;
        proxy_set_header Connection        $wb_conn_upgrade;
        proxy_connect_timeout 10s;
        proxy_read_timeout    300s;
        proxy_send_timeout    300s;
    }
}
```

改完：`sudo nginx -t && sudo systemctl reload nginx`

### 5. 验证

```bash
# 后端通不通
curl -s http://127.0.0.1:8082/api/health

# 子路径下 HTML 里引用的资源是不是相对路径
curl -s https://47.94.236.198/french/ | grep -E 'css/style.css|js/app.js'

# 静态资源与 API 是否 200
curl -s -o /dev/null -w "css  %{http_code}\n" https://47.94.236.198/french/css/style.css
curl -s -o /dev/null -w "js   %{http_code}\n" https://47.94.236.198/french/js/app.js
curl -s -o /dev/null -w "api  %{http_code}\n" https://47.94.236.198/french/api/courses
```

---

## 四、运行时限制 CPU 和内存

### 应用内（推荐，跨平台）

已经在 `main.go` 里实现，两个环境变量：

```bash
MEM_LIMIT_MB=420    # Go 堆软上限，超了 GC 就开始拼命回收
GOMAXPROCS=2        # Go 并行度，这才是 Go 程序的 CPU 阀门
```

`MEM_LIMIT_MB` 用的是 Go 1.19+ 的 `debug.SetMemoryLimit`：
它是**软限制**，不会直接杀进程，而是让 GC 更早、更频繁地工作，
把堆压在上限附近。这是 Go 程序限内存的正确姿势。

`GOMAXPROCS` 限制同时执行 Go 代码的线程数。比 `nice` 有效得多。

### Linux / systemd（cgroup）

见 `french.service` 的注释。核心是 `MemoryHigh` / `MemoryMax` /
`MemorySwapMax` / `CPUQuota`。

**一个必须知道的坑**：不要用 `ulimit -v` 给 Go 程序限内存。
Go 运行时会把大块虚拟地址空间保留在 arena 里，`ulimit -v` 限制的恰好是
虚拟地址空间总量，Go 一旦申请不到地址就直接 panic 退出 ——
看起来就像"内存限制把程序搞崩了"。cgroup 的 `memory.max` 限制的是
实际占用（RSS），对 Go 才是正确的。

### Windows（本地调试）

```powershell
.\tools\limit-process.ps1 -Exe .\build\francais.exe -WorkDir . -MemoryMB 700 -CpuCores 2
```

用 Job Object 设内存硬上限 + 限制可用的 CPU 核 + 降优先级。

---

## 五、词典导入的 CPU 尖峰

第一次启动（或词典更新后）会导入 38 万条词条 + 75 万条词形。
这期间 CPU 会打满（`top` 里能看到 100%+），**是一次性开销**。

导入完成后，词典指纹写进 `meta` 表，之后每次重启都会跳过导入：

```
INFO dictionary seed already present, skipping import
```

所以日常重启是秒级的。想压住导入期的 CPU，设 `GOMAXPROCS=1` 即可。

---

## 六、怎么把数据库拿回来

SQLite 单文件，SQLite 的库就是一个普通文件：

```bash
# 一致性备份（推荐，WAL 模式下也安全）
sqlite3 /home/admin/maintain/french/french.db ".backup /tmp/french-backup.db"
scp /tmp/french-backup.db 你的机器:

# 或者直接拷（服务运行时 WIP 数据可能在 -wal 里，用上面的 .backup 更保险）
```

轻量应用服务器自带的**快照**是更省事的方案：整机回滚，数据库一起回滚。

---

## 七、线上实例（47.94.236.198，2026-11-17 起）

| 项 | 值 |
|---|---|
| 服务器 | `47.94.236.198` |
| 部署目录 | `/home/admin/maintain/french`（git clone 自 GitHub） |
| 监听端口 | `8082`（nginx `location /french/` → `proxy_pass http://127.0.0.1:8082/`） |
| 服务管理 | `systemctl restart french` |
| 日志 | `journalctl -u french -f` |
| 词典种子 | `/home/admin/maintain/french/data/lexicon_seed.json`（164MB，**不入库**，需手动上传） |
| 扫描图 | `web/scans/`（410 张 jpg，被 `.gitignore` 的 `*.jpg` 排除，**需手动上传**） |

### 日常运维

```bash
ssh root@47.94.236.198
cd /home/admin/maintain/french
git pull                      # 拉最新代码
/usr/local/go/bin/go build -o bin/french .   # 重新编译（web/ 是 go:embed 的，必须重编译）
systemctl restart french
```

### 换库：从本地同步完整数据库

服务器上的库只有语法册和词汇册（77 章），
**《简明法语教程》8 章和全部答案都不在里面** —— 那些是脚本导入的，不在 embed 里。
换库必须用本地的完整库：

```bash
# 本地（Windows）：做一致性备份（服务在跑也安全）
python -c "
import sqlite3
src=sqlite3.connect('file:french.db?mode=ro',uri=True)
dst=sqlite3.connect('french-new.db')
src.backup(dst); dst.commit(); dst.close(); src.close()"

scp french-new.db root@47.94.236.198:/tmp/french-new.db.gz   # 建议 gzip 后传，约 35MB

# 服务器：停服、换库、删掉旧 WAL（关键！）
systemctl stop french
cd /home/admin/maintain/french
mv french.db french.db.old-$(date +%Y%m%d-%H%M%S)
rm -f french.db-wal french.db-shm      # 旧 WAL 配新库会损坏数据库，必须删
gunzip -c /tmp/french-new.db.gz > french.db
systemctl start french
```

### 坑：启动要 3 分钟，CPU 打满

服务启动时会**重播一遍章节播种**（85 章、3534 个内容块、1045 道练习、4786 道题），
日志刷屏 `chapter seeded ...`，这期间 CPU 打满 99%、RSS 涨到 250MB，
**期间 nginx 一直 502**，因为 `ListenAndServe` 在播种之后才执行。

这不是卡死，等它跑完就好。但如果连着重启多次，看着像死了。

### 坑：词典指纹丢了 → 每次启动重灌 38 万词条

词典靠 `meta` 表里的 `lexicon_seed_fingerprint` 判断是否已导入。
**这个指纹是在词典导入成功后才写入的**。

所以：从旧库（指纹功能上线前建的）直接部署时，
每次启动都会把 164MB 的词典重新灌一遍 —— 多花 3 分钟 CPU 和大量 WAL 写入。
日志长这样：

```
INFO dictionary seed imported entries=380616 forms=759450
```

跑完一次之后指纹就写进去了，之后重启是秒级：

```
INFO dictionary seed already present, skipping import fingerprint=c75516747377
```

实测：灌完之后 `systemctl restart french` → **2 秒**就绪，内存 10MB。


---

## 十、访问口令、学习时长与打卡墙（2026-11-18 新增）

### 访问口令

服务可选口令保护，由环境变量 `ACCESS_PASSCODE` 控制：

- **设了值**（如当前 `french2026`）：未登录访问任何页面会看到登录页；API 返回 401。
  输入口令后签发一个 HMAC 签名的 HttpOnly Cookie（`fp_auth`），30 天内免登录。
- **留空 / 删掉该行**：服务完全开放，和老版本行为一致。

要点：

- 口令即签名密钥：改口令后**旧 Cookie 全部自动失效**，无需改代码。
- `/api/health` 不受保护（给探针和探针脚本用）；`/api/auth/*` 三个端点也不需要登录。
- nginx 反代下页面路径是 `/french/`：登录页是自包含的单文件 HTML（内联 CSS/JS），
  相对路径提交 `api/auth/login`，因此不依赖任何 Mount 前缀，登录成功用 `location.replace('./')` 回首页。
- 前端 `api()` 封装会在收到 401 + `auth_required` 时自动 `location.reload()` 到登录页。
- 顶栏「退出」按钮仅在口令启用时出现（通过 `/api/auth/status` 判断）。

线上改口令（和其它所有配置一样，只改 `.env`）：

```bash
sudo $EDITOR /home/admin/maintain/french/.env   # ACCESS_PASSCODE=你的新口令
sudo systemctl restart french      # 不需要 daemon-reload
```

优先级：真实环境变量（shell / systemd / docker -e） > `.env` > 代码默认值。

### 学习时长统计口径

数据库里旧的 `attempt.started_at → submitted_at` **不能**当学习时长：
草稿可能挂十几个小时，相减会严重失真。因此改成前端**活跃计时**：

- 页面可见、当前是学习路由（章节 / 练习 / 课程 / 笔记页）、且 1 分钟内有真实交互
  （鼠标 / 键盘 / 滚动 / 触摸）时，每 5 秒累加一次；
- 攒够 20 秒或页面隐藏 / 离开时上报 `POST /api/study/ping`（`sendBeacon` 保证离开时也能送达）；
- 后端按 `Asia/Shanghai (+08:00)` 存到新表 `study_log(user_id, day, course_id, seconds)`，
  同一天同一课程累加，单次上限 600 秒防止异常客户端灌水。

**含义：时长从本版本部署后开始累计，旧数据不补。** 数据库 schema 由 `db.Exec(schemaSQL)`
在启动时执行 `CREATE TABLE IF NOT EXISTS`，老库无需手动迁移。

### 首页两个可视化

`GET /api/stats/activity?days=371` 一次返回首页所需全部数据：

- **每本书学习时长**：三张 GitHub 风格热力条，显示每门课的每日学习秒数和总时长；
- **每日工作量打卡墙**：53 周 × 7 天的热力格，强度 = 活跃分钟数 + 交卷次数 + 笔记条数。
  工作量把历史数据也算进去了：交卷来自 `attempt_revision.created_at`，笔记来自 `note.created_at`，
  都用固定 +08:00 归一到「本地日」（旧记录里 UTC 与 +08:00 混用，必须归一）。
- 顶部四个指标：累计学习、今日学习、连续打卡、活跃天数。

热力格 `title` 里有当日明细（学习时长 / 交卷数 / 笔记数 / 工作量），鼠标悬停可看。

---

## 十一、用 Makefile 编译与安装（推荐）

项目根有 `Makefile` 和 `french.service`，在 **Linux 服务器**的项目目录里执行：

```bash
make                    # 1. 编译 Linux amd64 二进制 → build/french（约 80MB，词典外置）
sudo make install       # 2. 装到 /home/admin/maintain/french/bin/french
                        #    + 首次生成 .env + 写 /etc/systemd/system/french.service
                        #    + daemon-reload + enable --now + 启动
make env                # 3. 看生效配置（口令打码）
make status             # 4. 看状态（确认 active (running)）
curl -s http://127.0.0.1:8082/api/health   # 冒烟：应返回 {"ok":true,...}
```

要点：

- **配置只在 `.env`**：`ADDR`（绑定地址）、`PORT`、`DB_PATH`、`ACCESS_PASSCODE`、运行时参数。
  `make install` 仅在目标目录还没有 `.env` 时按 `.env.example` 生成一份（权限 600），
  **升级时绝不覆盖线上已改过的配置**。改配置直接编辑 `.env` + `sudo systemctl restart french`。
- `make install` 里的 `check-root` 会检查 `id -u`，非 root 直接报错退出；用 `sudo make install`。
- 安装单元只对 `french.service` 做 2 处 sed 替换（目录 / 二进制名），
  端口和口令不再写进单元文件；单元通过 `EnvironmentFile=` 读 `.env`。
- `french.service` 本身就是可用的单元文件：直接 `cp french.service /etc/systemd/system/` 也能用，
  前提是目标目录已有 `.env`（用 `.env.example` 生成）。
- 首次生成 `.env` 时的覆盖示例（**仅首次有效**，之后要改 `.env`）：
  ```bash
  sudo make install DEST=/home/admin/maintain/french ADDR=127.0.0.1:9000 PASSCODE=我的口令
  ```
  注意 `DEST` 改变后，`$DEST/french.db` 必须已存在（先把库拷过去），否则服务会因空库启动但数据全无。
- **安装时自动处理词典**：`make install` 会建 `$(DEST)/data`，
  若本地有 `data/lexicon_seed.json` 且目标位置还没有，就装过去（只装一次，不覆盖）。
  目标位置由 `.env` 的 `LEXICON_SEED_FILE` 决定，默认 `$(DEST)/data/lexicon_seed.json`。
- 其它目标：`make restart`、`make stop`、`make logs`、`make env`、
  `sudo make uninstall`、`make clean`、`make dist`（本机交叉编译 + 打包，供没有 Go 的机器）。
  `make uninstall` 保留 `.env`、二进制、词典和数据库，只停服并删单元文件。
- Makefile 仍固定 `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`；在 Linux 上就是原生编译，
  在 Windows/macOS 上执行 `make dist` 也能产出 Linux 二进制。
