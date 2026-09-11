APP := build/SSH Proxy Manager.app
BIN := $(APP)/Contents/MacOS/SSHProxyManager
SPM := $(APP)/Contents/MacOS/spm

.PHONY: build run test clean cli

build:
	./build.sh

run: build
	open "$(APP)"

test:
	go test ./...

cli: build
	"$(SPM)" list

clean:
	@rm -rf build && echo "removed build/"
