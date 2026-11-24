# 项目结构说明

> 2026-11-17 目录重组后更新。此文说明"什么文件放在哪、为什么这么放"。

## 运行时配置在哪

**在项目根的 `.env`**（模板 `.env.example`，已提交；`.env` 本身含口令，已 gitignore）。
`config.go: loadDotEnv()` 在 `main()` 最开始加载它，优先级：真实环境变量 > `.env` > 代码默认值。
systemd 也用 `EnvironmentFile=` 指向同一文件，因此**端口、绑定地址、数据库、口令只改一处**：

```
ADDR=:8082                      # 完整监听地址；127.0.0.1:8082 = 只给 nginx 反代
PORT=8082                       # ADDR 为空时的简写
DB_PATH=/home/admin/maintain/french/french.db
ACCESS_PASSCODE=<强口令>         # 公开仓库只放占位符；留空 = 拒绝启动
SPEAKING_INGEST_TOKEN=<随机串>   # 每日练习发布专用，留空则禁用发布接口
MEM_LIMIT_MB=420 / GOMAXPROCS=2 / GOGC=50
```

改完 `sudo systemctl restart french`。本地直接跑二进制时也读当前目录的 `.env`
（可用 `ENV_FILE=/path/to/.env` 指定别处）。旧 Windows daemon 的 `.env`
（`french-daemon/.env`，含 APP_PORT/GATEWAY_*）是宿主的调度配置，与本文件无关。

## 目录结构

```
Français Prograssive/
├── main.go / store.go / handlers.go / lexicon.go / grading.go / attempts.go
│   ├── config.go               .env 加载 + 监听地址解析（2026-11-18 新增）
│   ├── stats.go                学习时长与每日工作量统计 API（2026-11-18 新增）
│   ├── auth.go                 访问口令鉴权（ACCESS_PASSCODE，2026-11-18 新增）
│   └── *_test.go               Go 源码（单包项目，源码必须在包根）
├── schema.sql              go:embed
├── go.mod / go.sum
├── french.db(+wal/shm)     ★ 唯一运行时数据库，必须在项目根
├── .env.example            【模板】运行配置，提交进仓库
├── .env                    本机配置（含口令，gitignore；VPS 由 make install 生成）
├── Makefile / french.service   Linux 编译 + systemd 安装（配置走 EnvironmentFile=.env）
├── deploy.ps1 / 发布.ps1   发布脚本
├── PROGRESS.md             录入进度
│
├── data/
│   ├── courses/            【编译期资源】go:embed 进二进制
│   │   ├── chapter01.json      语法第 1 讲
│   │   ├── vocabulaire01.json  词汇第 1 讲
│   │   └── chapters/           第 2 讲起每讲一个 json（gNN/vNN）
│   ├── lexicon_seed.json    【运行时数据】164MB 词典种子，启动时流式导入数据库（不进二进制）
│   ├── import/             【导入中间产物】不进二进制，可重新生成
│   │   ├── answers/            语法答案（gNN.json + raw/ + over/）
│   │   ├── answers_vocab/      词汇答案（vNN.json + raw/ + over/）
│   │   └── jianming/           简明法语教程转录与解析结果
│   ├── lexicon/            【词典辅助】手动补充、快速索引、跳转表
│   │   ├── lexicon_manual.json
│   │   ├── lexicon_quick.json
│   │   ├── redirects.jsonl
│   │   └── course_terms.txt
│   └── source/             【原始语料】566MB，可由 tools/fetch_kaikki.py 重下
│
├── web/                    【前端资源】go:embed + 运行时静态文件
│   ├── index.html  css/  js/  scans/
│   │   └── js/stats-view.js / study-tracker.js   首页热力图 / 活跃计时（新增）
├── tools/                  Python 导入与校验脚本
├── docs/                   设计文档
├── build/                  构建产物（不提交）
└── .workbuddy/             WorkBuddy 工作区数据（不提交）
```

## 三个必须记住的约束

1. **`french.db` 只能在项目根。** `deploy.ps1` 的 `$Db = Join-Path $Project 'french.db'`
   和 daemon 的 `cd $APP_ROOT` 都写死了这个位置。
2. **`data/courses/` 是编译期资源，`data/lexicon_seed.json` 不是。**
   课程改目录要同步 `store.go` 的 `//go:embed` / `fs.Glob` 路径并重新编译；
   词典是运行时文件，由 `.env` 的 `LEXICON_SEED_FILE` 指定，换路径不用重编译。
   **绝不要把词典 embed 进二进制**（曾导致 243MB 二进制、VPS 冷编译超 5 分钟）。
3. **改任何 data 下的路径，都要同步 `tools/*.py` 的引用**（共 7 个脚本）。

## 数据从哪来

```
法语书扫描件 → data/import/answers/raw/pNNN.txt
             → tools/parse_corriges.py → data/import/answers/gNN.json
             → tools/import_answers.py → french.db

Kaikki 原始语料 → data/source/kaikki.org-dictionary-French.jsonl
                → tools/build_lexicon.py → data/lexicon_seed.json
                → 运行时由 LEXICON_SEED_FILE 指向 → 启动时流式导入 french.db
```

## 构建与发布

```bash
go vet ./...                      # 语法与静态检查
go build -o build/francais.exe .  # 产物进 build/
./deploy.ps1 -SkipTests           # 备份库 → 编译 → 换二进制 → 起服务 → 冒烟
```

发布后二进制在 `french-daemon/bin/francais.exe`（约 80MB；164MB 词典不进二进制，
运行时从 `LEXICON_SEED_FILE` 指向的文件导入数据库）。

**启动服务务必用 supervisor**（它会正确 cd 到 APP_ROOT）：

```bash
cd french-daemon && bash supervisor.sh service
```

直接跑 `french-daemon/bin/francais.exe` 会在 daemon 目录建空库。

启动前若发现 `french-daemon/bin/stop.flag` 存在，supervisor 会直接退出 —— 先删掉它。
