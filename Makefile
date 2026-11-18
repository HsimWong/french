# Français progressif — Linux 编译 / 安装
#
# 常用：
#   make                 编译 Linux 二进制到 build/francais（词典嵌入，默认）
#   make noseed          编译不嵌词典的版本（约 80MB，需磁盘上的 data/lexicon_seed.json）
#   sudo make install    安装二进制 + systemd 服务，并启动
#   make status          查看服务状态
#   make logs            跟踪日志
#   sudo make uninstall  停服并移除单元文件（不删二进制和数据库）
#
# 配置统一在 .env（ADDR=监听地址、PORT、DB_PATH、ACCESS_PASSCODE ...）。
# make install 只在目标目录还没有 .env 时按下面的变量生成一份；已存在就保留，
# 之后改配置直接编辑 .env + sudo systemctl restart french。
# 变量覆盖示例：
#   make install DEST=/opt/francais ADDR=127.0.0.1:9000 PASSCODE=另一个口令

SHELL      := /bin/sh
GO         ?= go
DEST       ?= /home/admin/maintain/french
BIN_NAME   ?= french
ADDR       ?= :8082
PORT       ?= 8082
PASSCODE   ?= french2026
BUILD_DIR  ?= build
BIN        ?= $(BUILD_DIR)/$(BIN_NAME)
SERVICE    ?= $(BIN_NAME).service
SYSTEMD    ?= /etc/systemd/system/$(SERVICE)
ENV_FILE   ?= $(DEST)/.env
ENV_EXAMPLE ?= .env.example

# 交叉编译：在 Windows/macOS 上 make 也产出 Linux amd64 二进制；
# 在 Linux 本机编译则原生即可，这里显式写死目标平台保证一致。
export GOOS   := linux
export GOARCH ?= amd64
export CGO_ENABLED := 0

.PHONY: all build noseed install uninstall restart stop status logs env clean help check-root

all: build

## build: 默认构建，词典嵌入二进制（单文件部署，约 240MB，需要 data/lexicon_seed.json）
build:
	@mkdir -p $(BUILD_DIR)
	$(GO) build -trimpath -o $(BIN) .
	@ls -lh $(BIN)

## noseed: 词典不嵌入，运行时从磁盘读（二进制约 80MB）
noseed:
	@mkdir -p $(BUILD_DIR)
	$(GO) build -trimpath -tags noseed -o $(BIN) .
	@ls -lh $(BIN)

## install: 编译 + 装二进制 + 装服务单元 + 启动（需 root）
install: build check-root
	install -d $(DEST)/bin
	install -m 0755 $(BIN) $(DEST)/bin/$(BIN_NAME)
	@# .env 是唯一配置源；首次安装时生成，之后升级绝不覆盖线上改动
	@if [ -f "$(ENV_FILE)" ]; then \
		echo "保留现有配置 $(ENV_FILE)"; \
	else \
		sed -e 's|^ADDR=.*|ADDR=$(ADDR)|' \
		    -e 's|^PORT=.*|PORT=$(PORT)|' \
		    -e 's|^ACCESS_PASSCODE=.*|ACCESS_PASSCODE=$(PASSCODE)|' \
		    -e 's|/home/admin/maintain/french|$(DEST)|g' \
		    $(ENV_EXAMPLE) > $(ENV_FILE); \
		chmod 600 $(ENV_FILE); \
		echo "已生成 $(ENV_FILE)（ADDR=$(ADDR) PORT=$(PORT)）"; \
	fi
	@# 单元文件只随 DEST / 二进制名渲染；端口、口令不再写死在 service 里
	sed -e 's|/home/admin/maintain/french|$(DEST)|g' \
	    -e 's|/bin/french|/bin/$(BIN_NAME)|g' \
	    $(SERVICE) > $(SYSTEMD)
	systemctl daemon-reload
	systemctl enable --now $(SERVICE)
	@echo "已安装并启动 $(SERVICE)；配置在 $(ENV_FILE)"
	@$(MAKE) --no-print-directory status

## uninstall: 停服并移除单元文件，保留二进制和数据库
uninstall: check-root
	-systemctl disable --now $(SERVICE)
	rm -f $(SYSTEMD)
	systemctl daemon-reload
	@echo "已卸载 $(SERVICE)（二进制与数据库保留在 $(DEST)）"

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
		sed -e 's|^ACCESS_PASSCODE=.*|ACCESS_PASSCODE=***|' $(ENV_FILE); \
	else \
		echo "(尚未生成；sudo make install 会按 .env.example 生成)"; \
	fi

clean:
	rm -rf $(BUILD_DIR)

check-root:
	@if [ "$$(id -u)" != "0" ]; then echo "请用 sudo 执行此目标（需要写 /etc/systemd/system 并操作 systemctl）" >&2; exit 1; fi

help:
	@sed -n '2,18p' $(MAKEFILE_LIST)
