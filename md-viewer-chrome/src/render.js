import { Marked } from "marked";
import { gfmHeadingId } from "marked-gfm-heading-id";
import DOMPurify from "dompurify";

const marked = new Marked({ gfm: true, breaks: false }, gfmHeadingId({ prefix: "" }));

// GitHub 自己的 markdown 也做清洗，这里按同样的口径：剥掉可执行内容、脚本化 URL，
// 保留 GFM 需要的内联标签（表格、任务列表、图片、kbd 等）。
const PURIFY_CONFIG = {
  FORBID_TAGS: ["script", "style", "iframe", "frame", "object", "embed", "form", "link", "meta", "base"],
  FORBID_ATTR: ["onerror", "onload", "onclick", "onmouseover", "onfocus", "srcset"],
  ALLOW_DATA_ATTR: false,
  USE_PROFILES: { html: true },
};

const ABSOLUTE_URL = /^[a-z][a-z0-9+.-]*:/i;

export function renderMarkdown(source, baseUrl) {
  const html = marked.parse(source ?? "");
  const clean = DOMPurify.sanitize(html, PURIFY_CONFIG);
  return absolutize(clean, baseUrl);
}

// 相对链接/图片按「原文件所在 URL」解析：本地文件指向同目录，raw 指向仓库同目录。
export function absolutize(html, baseUrl) {
  if (!baseUrl) return html;
  const template = document.createElement("template");
  template.innerHTML = html;

  for (const anchor of template.content.querySelectorAll("a[href]")) {
    const href = anchor.getAttribute("href") ?? "";
    const resolved = resolve(href, baseUrl);
    anchor.setAttribute("href", resolved);
    if (/^https?:/i.test(resolved)) {
      anchor.setAttribute("target", "_blank");
      anchor.setAttribute("rel", "noopener noreferrer");
    }
  }

  for (const image of template.content.querySelectorAll("img[src]")) {
    image.setAttribute("src", resolve(image.getAttribute("src") ?? "", baseUrl));
  }
  return template.innerHTML;
}

function resolve(value, baseUrl) {
  const trimmed = value.trim();
  if (trimmed === "" || trimmed.startsWith("#")) return value;
  if (ABSOLUTE_URL.test(trimmed)) return value;
  try {
    return new URL(trimmed, baseUrl).href;
  } catch {
    return value;
  }
}
