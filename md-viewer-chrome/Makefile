.PHONY: install build watch test e2e clean

install:
	npm install

build:
	node scripts/build.mjs

watch:
	node scripts/build.mjs --watch

test:
	node --test

e2e: build
	node test/e2e.mjs

clean:
	rm -rf dist
