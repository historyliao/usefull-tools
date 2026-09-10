APP := build/SSH Mount Manager.app
INSTALLED := /Applications/SSH Mount Manager.app
BIN := $(INSTALLED)/Contents/MacOS/SSHMountManager
LSREGISTER := /System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister

.PHONY: build run install cli clean

build:
	./build.sh

run: install
	open "$(INSTALLED)"

install: build
	-pkill -f 'SSHMountManager' >/dev/null 2>&1 || true
	@sleep 1
	ditto "$(APP)" "$(INSTALLED)"
	"$(LSREGISTER)" -f "$(INSTALLED)"
	-"$(LSREGISTER)" -u "$(APP)"
	-rm -rf "$(APP)"
	@echo "已安装到 $(INSTALLED)（旧实例已停止；仓库内的构建副本已移除，启动台只保留一份）"

cli: install
	"$(BIN)" --cli list

clean:
	@rm -rf build && echo "removed build/"
