import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test, before } from "node:test";
import { JSDOM } from "jsdom";

let renderMarkdown;

before(async () => {
  const dom = new JSDOM("<!doctype html><html><body></body></html>", {
    url: "https://example.com/docs/guide/README.md",
  });
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  globalThis.Node = dom.window.Node;
  ({ renderMarkdown } = await import("../src/render.js"));
});

const render = (markdown) => renderMarkdown(markdown, "https://example.com/docs/guide/README.md");

test("GFM：表格、任务列表、删除线、自动链接", () => {
  const html = render(
    [
      "| 名称 | 值 |",
      "| --- | --- |",
      "| a | 1 |",
      "",
      "- [x] 已完成",
      "- [ ] 未完成",
      "",
      "~~删掉~~ 和 https://example.org/x",
    ].join("\n")
  );

  assert.match(html, /<table>/);
  assert.match(html, /<th>名称<\/th>/);
  assert.match(html, /<input checked="" disabled="" type="checkbox">/);
  assert.match(html, /<input disabled="" type="checkbox">/);
  assert.match(html, /<del>删掉<\/del>/);
  assert.match(html, /<a href="https:\/\/example\.org\/x"/);
});

test("围栏代码块保留语言标记", () => {
  const html = render("```go\nfmt.Println(\"hi\")\n```");
  assert.match(html, /<code class="language-go">/);
});

test("标题锚点与 GitHub slug 规则一致", () => {
  const html = render("# Hello World\n\n## 中文 标题\n\n### Duplicate\n\n### Duplicate");
  assert.match(html, /id="hello-world"/);
  assert.match(html, /id="中文-标题"/);
  assert.match(html, /id="duplicate"/);
  assert.match(html, /id="duplicate-1"/);
});

test("危险 HTML 与脚本化 URL 被清洗", () => {
  const html = render(
    [
      "<script>alert(1)</script>",
      "<img src=x onerror=\"alert(1)\">",
      "<iframe src=\"https://evil.example\"></iframe>",
      "[点我](javascript:alert(1))",
      "<a href=\"javascript:alert(2)\">x</a>",
    ].join("\n\n")
  );

  assert.doesNotMatch(html, /<script/i);
  assert.doesNotMatch(html, /onerror/i);
  assert.doesNotMatch(html, /<iframe/i);
  assert.doesNotMatch(html, /javascript:/i);
});

test("相对链接与图片按原文件 URL 解析", () => {
  const html = render("[同目录](./other.md)\n\n![图](../img/a.png)\n\n[锚点](#section)");
  assert.match(html, /href="https:\/\/example\.com\/docs\/guide\/other\.md"/);
  assert.match(html, /src="https:\/\/example\.com\/docs\/img\/a\.png"/);
  assert.match(html, /href="#section"/);
});

test("外链补 target/rel，内链不动", () => {
  const html = render("[外](https://other.example/a)");
  assert.match(html, /target="_blank"/);
  assert.match(html, /rel="noopener noreferrer"/);
});

test("fixture：整篇文档能渲染且行内 HTML 白名单有效", async () => {
  const source = await readFile(new URL("./fixtures/gfm.md", import.meta.url), "utf8");
  const html = render(source);
  assert.match(html, /<h1 id="gfm-示例">/);
  assert.match(html, /<kbd>/);
  assert.doesNotMatch(html, /<script/i);
});
