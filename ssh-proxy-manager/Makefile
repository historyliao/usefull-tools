STAGE := /tmp/ssh-proxy-manager-build
APP := $(STAGE)/SSH Proxy Manager.app
INSTALLED := /Applications/SSH Proxy Manager.app
BIN := $(INSTALLED)/Contents/MacOS/SSHProxyManager
SPM := $(INSTALLED)/Contents/MacOS/spm
LSREGISTER := /System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister

.PHONY: build run install test clean cli

build:
	./build.sh "$(STAGE)"

install: build
	-pkill -f 'SSHProxyManager' >/dev/null 2>&1 || true
	@sleep 1
	ditto "$(APP)" "$(INSTALLED)"
	"$(LSREGISTER)" -f "$(INSTALLED)"
	@echo "已安装到 $(INSTALLED)（只保留这一份，启动台不会再出现重复）"

run: install
	open "$(INSTALLED)"

test:
	go test ./...

cli: install
	"$(SPM)" list

clean:
	@rm -rf "$(STAGE)" && echo "removed $(STAGE)"
