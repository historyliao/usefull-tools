// content script 自己不能改注入样式（chrome.scripting 只对扩展上下文开放），
// 所以由它在命中 Markdown 时喊一声，service worker 再把样式打进那个标签页。
chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (message?.type !== "md-viewer:styles") return false;
  const tabId = sender.tab?.id;
  if (tabId === undefined) {
    sendResponse({ ok: false, error: "no tab" });
    return false;
  }
  chrome.scripting
    .insertCSS({ files: ["content.css"], target: { tabId, frameIds: [sender.frameId ?? 0] } })
    .then(
      () => sendResponse({ ok: true }),
      (error) => sendResponse({ ok: false, error: String(error) })
    );
  return true;
});

// 本地文件渲染需要用户手动打开「允许访问文件网址」，装完先提醒一次。
async function checkFileAccess() {
  const allowed = await chrome.extension.isAllowedFileSchemeAccess();
  if (!allowed) {
    await chrome.action.setTitle({
      title: "Markdown Viewer：本地 file:// 文件需要到 chrome://extensions 打开「允许访问文件网址」",
    });
    console.warn(
      "[md-viewer] 本地 file:// 文件暂不可用：请到 chrome://extensions 找到本扩展，打开「允许访问文件网址」。"
    );
  }
}

chrome.runtime.onInstalled.addListener(() => void checkFileAccess());
chrome.runtime.onStartup.addListener(() => void checkFileAccess());
