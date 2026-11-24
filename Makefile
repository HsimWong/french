# Français progressif — Linux 编译 / 打包 / 安装
#
# 词典永远不进二进制：它是运行时数据文件 data/lexicon_seed.json，
# 由 .env 的 LEXICON_SEED_FILE 指定路径，启动时流式导入数据库。
# 因此**在 VPS 上直接 make 也能很快编完**（二进制约 80MB），不需要交叉编译。
#
# 常用：
#   make                 编译 Linux amd64 二进制（约 80MB，词典外置）
#   sudo make install    装二进制 + 词典 + 服务单元 + 首次生成 .env + 启动（需 root）
#   make env             查看生效配置（口令打码）
#   make status/logs     查看状态 / 跟踪日志
#   sudo make uninstall  停服并移除单元文件（不删二进制、词典和数据库）
#   make dist            可选：本机交叉编译并打包，供没有 Go 环境的机器使用
#
# 配置统一在 .env（ADDR=监听地址、PORT、DB_PATH、ACCESS_PASSCODE、LEXICON_SEED_FILE）。
# make install 只在目标目录还没有 .env 时按下面的变量生成一份；已存在就保留，
# 之后改配置直接编辑 .env + sudo systemctl restart french。
# 变量覆盖示例：
#   make install DEST=/opt/francais ADDR=127.0.0.1:9000 PASSCODE=另一个口令 INGEST_TOKEN=专用发布令牌

SHELL      := /bin/sh
GO         ?= go
DEST       ?= /home/admin/maintain/french
BIN_NAME   ?= french
ADDR       ?= :8082
PORT       ?= 8082
PASSCODE   ?= CHANGE_ME_TO_A_LONG_RANDOM_PASSCODE
# 仅用于每日练习发布；留空则禁用该接口。不要写进公开命令历史/文档。
INGEST_TOKEN ?=
BUILD_DIR  ?= bin
BIN        ?= $(BUILD_DIR)/$(BIN_NAME)
SERVICE    ?= $(BIN_NAME).service
SYSTEMD    ?= /etc/systemd/system/$(SERVICE)
ENV_FILE   ?= $(DEST)/.env
ENV_EXAMPLE ?= .env.example
SEED_FILE   ?= data/lexicon_seed.json
REMOTE_SEED ?= $(DEST)/data/lexicon_seed.json
DIST_DIR   ?= $(BUILD_DIR)/dist
VERSION    ?= $(shell date +%Y%m%d-%H%M%S)
TARBALL    ?= $(DIST_DIR)/french-$(VERSION).tar.gz

# 交叉编译：在 Windows/macOS 上 make 也产出 Linux amd64 二进制；
# 在 Linux 本机编译则原生即可，这里显式写死目标平台保证一致。
export GOOS   := linux
export GOARCH ?= amd64
export CGO_ENABLED := 0

.PHONY: all build noseed dist install uninstall restart stop status logs env clean help check-root

all: build

## build: 编译二进制，词典外置（约 80MB；VPS 上直接跑）
build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build -trimpath -o $(BIN) .
	@ls -lh $(BIN)

## noseed: 兼容旧脚本的别名；现在只有这一种构建方式
noseed: build
	@echo "提示：词典本来就外置，noseed 只是 build 的别名"

## dist: 本机编译 + 打包（二进制 + 词典 + .env.example + 服务单元 + sha256）
dist: build
	@mkdir -p $(DIST_DIR)/stage/data
	cp $(BIN) $(DIST_DIR)/stage/$(BIN_NAME)
	cp $(ENV_EXAMPLE) $(SERVICE) $(DIST_DIR)/stage/
	@if [ -f $(SEED_FILE) ]; then \
		cp $(SEED_FILE) $(DIST_DIR)/stage/data/lexicon_seed.json; \
	else \
		echo "警告：找不到 $(SEED_FILE)，包里不含词典"; \
	fi
	tar -czf $(TARBALL) -C $(DIST_DIR)/stage .
	(cd $(DIST_DIR) && sha256sum $(notdir $(TARBALL)) > $(notdir $(TARBALL)).sha256)
	@ls -lh $(TARBALL) $(TARBALL).sha256
	@echo "上传示例：scp $(TARBALL) root@VPS:$(DEST)/"

## install: 装二进制 + 词典 + 服务单元 + 首次生成 .env + 启动（需 root）
install: build check-root
	install -d $(DEST)/bin $(DEST)/data
	install -m 0755 $(BIN) $(DEST)/bin/$(BIN_NAME)
	@# 词典只在目标位置缺失时安装一次；已有新版词典不覆盖
	@if [ -f $(SEED_FILE) ] && [ ! -f $(REMOTE_SEED) ]; then \
		install -m 0644 $(SEED_FILE) $(REMOTE_SEED); \
		echo "已安装词典 $(REMOTE_SEED)"; \
	fi
	@if [ ! -f $(REMOTE_SEED) ]; then \
		echo "警告：$(REMOTE_SEED) 不存在；若数据库已有词典仍可正常启动，但无法重新导入"; \
	fi
	@# .env 是唯一配置源；首次安装时生成，之后升级绝不覆盖线上改动
	@if [ -f "$(ENV_FILE)" ]; then \
		echo "保留现有配置 $(ENV_FILE)"; \
	else \
		sed -e 's|^ADDR=.*|ADDR=$(ADDR)|' \
		    -e 's|^PORT=.*|PORT=$(PORT)|' \
		    -e 's|^ACCESS_PASSCODE=.*|ACCESS_PASSCODE=$(PASSCODE)|' \
		    -e 's|^SPEAKING_INGEST_TOKEN=.*|SPEAKING_INGEST_TOKEN=$(INGEST_TOKEN)|' \
		    -e 's|/home/admin/maintain/french|$(DEST)|g' \
		    $(ENV_EXAMPLE) > $(ENV_FILE); \
		chmod 600 $(ENV_FILE); \
		echo "已生成 $(ENV_FILE)（ADDR=$(ADDR) PORT=$(PORT)）"; \
	fi
	@# 单元文件只随 DEST / 二进制名渲染；端口、口令、词典路径都不写死在 service 里
	sed -e 's|/home/admin/maintain/french|$(DEST)|g' \
	    -e 's|/bin/french|/bin/$(BIN_NAME)|g' \
	    $(SERVICE) > $(SYSTEMD)
	systemctl daemon-reload
	systemctl enable --now $(SERVICE)
	@echo "已安装并启动 $(SERVICE)；配置在 $(ENV_FILE)"
	@$(MAKE) --no-print-directory status

## uninstall: 停服并移除单元文件，保留二进制、词典和数据库
uninstall: check-root
	-systemctl disable --now $(SERVICE)
	rm -f $(SYSTEMD)
	systemctl daemon-reload
	@echo "已卸载 $(SERVICE)（二进制、词典、数据库与 .env 均保留在 $(DEST)）"

restart: check-root
	systemctl restart $(SERVICE)
	@$(MAKE) --no-print-directory status

stop: check-root
	systemctl stop $(SERVICE)

status:
	@systemctl --no-pager --full status $(SERVICE) 2>&1 || true

logs:
	journalctl -u $(SERVICE) -f

## env: 查看生效的配置（隐藏口令）
env:
	@echo "配置文件: $(ENV_FILE)"
	@if [ -f "$(ENV_FILE)" ]; then \
		awk -F= '/^(ACCESS_PASSCODE|SPEAKING_INGEST_TOKEN)=/{ \
			if (length($$2)==0) print $$1 "=(空)"; \
			else print $$1 "=***"; next } {print}' $(ENV_FILE); \
	else \
		echo "(尚未生成；sudo make install 会按 .env.example 生成)"; \
	fi

clean:
	rm -rf $(BUILD_DIR)

check-root:
	@if [ "$$(id -u)" != "0" ]; then echo "请用 sudo 执行此目标（需要写 /etc/systemd/system 并操作 systemctl）" >&2; exit 1; fi

help:
	@sed -n '2,25p' $(MAKEFILE_LIST)
