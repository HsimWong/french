# 项目结构说明

> 2026-11-17 目录重组后更新。此文说明"什么文件放在哪、为什么这么放"。

## 运行时配置在哪

**不在本项目里。** 运行时配置在 `C:/Users/WangXin/WorkBuddy/french-daemon/.env`：

```
APP_ROOT="C:/Users/WangXin/WorkBuddy/Français Prograssive"
APP_PORT=8080
DB_PATH=french.db          # 相对 APP_ROOT，supervisor 会先 cd 到 APP_ROOT
GATEWAY_URL=...            # WorkBuddy 网关固定入口
GATEWAY_TOKEN=...
```

daemon 用 `cd "$APP_ROOT"` 后再执行，所以 **`french.db` 必须待在项目根**。
误在别处启动服务会凭空建一个空库（本次重组时踩过：服务显示"答案 0 条"就是这个原因）。

## 目录结构

```
Français Prograssive/
├── main.go / store.go / handlers.go / lexicon.go / grading.go / attempts.go
│   ├── stats.go                学习时长与每日工作量统计 API（2026-11-18 新增）
│   ├── auth.go                 访问口令鉴权（ACCESS_PASSCODE，2026-11-18 新增）
│   └── *_test.go               Go 源码（单包项目，源码必须在包根）
├── schema.sql              go:embed
├── go.mod / go.sum
├── french.db(+wal/shm)     ★ 唯一运行时数据库，必须在项目根
├── deploy.ps1 / 发布.ps1   发布脚本
├── PROGRESS.md             录入进度
│
├── data/
│   ├── courses/            【编译期资源】go:embed 进二进制
│   │   ├── chapter01.json      语法第 1 讲
│   │   ├── vocabulaire01.json  词汇第 1 讲
│   │   └── chapters/           第 2 讲起每讲一个 json（gNN/vNN）
│   ├── lexicon_seed.json    【编译期资源】164MB 词典种子，embed 进二进制
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
2. **`data/courses/` 和 `data/lexicon_seed.json` 是编译期资源。**
   改了目录必须同步 `store.go` 的 `//go:embed` 路径和 `fs.Glob` 路径，然后重新编译。
3. **改任何 data 下的路径，都要同步 `tools/*.py` 的引用**（共 7 个脚本）。

## 数据从哪来

```
法语书扫描件 → data/import/answers/raw/pNNN.txt
             → tools/parse_corriges.py → data/import/answers/gNN.json
             → tools/import_answers.py → french.db

Kaikki 原始语料 → data/source/kaikki.org-dictionary-French.jsonl
                → tools/build_lexicon.py → data/lexicon_seed.json
                → go:embed → 二进制 → 启动时导入 french.db
```

## 构建与发布

```bash
go vet ./...                      # 语法与静态检查
go build -o build/francais.exe .  # 产物进 build/
./deploy.ps1 -SkipTests           # 备份库 → 编译 → 换二进制 → 起服务 → 冒烟
```

发布后二进制在 `french-daemon/bin/francais.exe`（约 243MB，含 164MB 词典）。

**启动服务务必用 supervisor**（它会正确 cd 到 APP_ROOT）：

```bash
cd french-daemon && bash supervisor.sh service
```

直接跑 `french-daemon/bin/francais.exe` 会在 daemon 目录建空库。

启动前若发现 `french-daemon/bin/stop.flag` 存在，supervisor 会直接退出 —— 先删掉它。
