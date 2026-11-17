# 法语学习平台 —— VPS 部署指南（Linux）

> 面向 2G 内存的小机器。整套方案的目标：**内存可控、CPU 可限、随时能拿到数据库文件**。

---

## 一、你 Linux 上编译失败的原因

```
store.go:36:12: pattern data/lexicon_seed.json: no matching files found
```

`data/lexicon_seed.json`（164MB 词典种子）原本用 `go:embed` 硬编进二进制。
这个文件**没有跟着源码传过去**（它同时也在 `.gitignore` 里，`git clone` 也不会带它），
于是 `go:embed` 找不到匹配文件，编译直接失败。

### 两种解法

**A. 补齐词典文件**（保留 embed）

```bash
mkdir -p data
scp 你的机器:Français\ Prograssive/data/lexicon_seed.json data/
go build -o bin/french .
```

产物 **243MB**，词典在二进制里，单文件部署。

**B. 词典走磁盘（推荐）**

```bash
go build -tags noseed -o bin/french .
```

产物 **71MB**。词典文件放在 `data/lexicon_seed.json`，
程序启动时用 `LEXICON_SEED_FILE` 指定路径，**流式**读进数据库。

两种构建在运行时行为完全一致，区别只是词典从哪来。

---

## 二、为什么 `/french/` 访问不到

有三个叠加的原因，逐个排查：

### 1. 服务根本没在监听（你贴的 `ss` 输出证实了这一点）

```
tcp  0  0 127.0.0.1:8081   0.0.0.0:*   LISTEN   617/finance
```

只有 finance。**8082 上什么都没有**，nginx 转过去必然 502。

### 2. 端口不一致

直接跑 `./bin/french` 而不设 `PORT` 环境变量时，程序监听**默认的 8080**
（见 `main.go`：`addr := os.Getenv("PORT")`，为空则用 `"8080"`）。
你的 nginx 转的是 8082。

**要么 nginx 改 8080，要么启动时设 `PORT=8082`。**

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
sudo mkdir -p /opt/francais/data
sudo chown -R $USER /opt/francais

# 传源码、构建产物、词典、数据库
rsync -av --exclude build --exclude .git ./ root@47.94.236.198:/opt/francais/
```

**最关键的是数据库文件**。不传的话，服务会新建一个空库，
只含 embed 里的两门课程（语法 52 章 + 词汇 25 章 = 77 章），
**《简明法语教程》那 8 章会全部丢失**（它是走 Python 脚本导入的，不在 embed 里）。

```bash
scp french.db root@47.94.236.198:/opt/francais/
```

传输时带上 WAL：

```bash
# 停服后再拷最干净，或者用 sqlite3 的 .backup 命令做一致性备份
sqlite3 french.db ".backup /tmp/french.db" && scp /tmp/french.db root@...:/opt/francais/
```

### 2. 构建

```bash
cd /opt/francais
# 方式 B（词典走磁盘，二进制 71MB）
go build -tags noseed -o bin/french .
# 方式 A（词典嵌入，二进制 243MB）
# go build -o bin/french .
```

### 3. 配 systemd（带资源限制）

```bash
cp deploy/francais.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now francais
sudo systemctl status francais
sudo journalctl -u francais -f
```

单元里已经设好：

| 项 | 值 | 作用 |
|---|---|---|
| `PORT` | 8082 | 和 nginx 对齐 |
| `DB_PATH` | `/opt/francais/french.db` | 绝对路径，不受工作目录影响 |
| `MEM_LIMIT_MB` | 420 | Go 堆软上限（`debug.SetMemoryLimit`） |
| `GOMAXPROCS` | 2 | Go 并行度，真正的 CPU 阀门 |
| `GOGC` | 50 | 让 GC 更早介入 |
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

见 `deploy/francais.service` 的注释。核心是 `MemoryHigh` / `MemoryMax` /
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
sqlite3 /opt/francais/french.db ".backup /tmp/french-backup.db"
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
| 服务管理 | `systemctl restart francais` |
| 日志 | `journalctl -u francais -f` |
| 词典种子 | `/home/admin/maintain/french/data/lexicon_seed.json`（164MB，**不入库**，需手动上传） |
| 扫描图 | `web/scans/`（410 张 jpg，被 `.gitignore` 的 `*.jpg` 排除，**需手动上传**） |

### 日常运维

```bash
ssh root@47.94.236.198
cd /home/admin/maintain/french
git pull                      # 拉最新代码
/usr/local/go/bin/go build -o bin/french .   # 重新编译（web/ 是 go:embed 的，必须重编译）
systemctl restart francais
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
systemctl stop francais
cd /home/admin/maintain/french
mv french.db french.db.old-$(date +%Y%m%d-%H%M%S)
rm -f french.db-wal french.db-shm      # 旧 WAL 配新库会损坏数据库，必须删
gunzip -c /tmp/french-new.db.gz > french.db
systemctl start francais
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

实测：灌完之后 `systemctl restart francais` → **2 秒**就绪，内存 10MB。

