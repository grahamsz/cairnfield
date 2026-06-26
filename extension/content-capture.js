(function () {
  let singleFileModulePromise;

  function singleFileModule() {
    if (!singleFileModulePromise) {
      singleFileModulePromise = import(chrome.runtime.getURL("single-file/single-file.js"));
    }
    return singleFileModulePromise;
  }

  async function captureSingleFile(doc = document, win = window) {
    const singleFile = await singleFileModule();
    const pageData = await singleFile.getPageData({
      blockScripts: true,
      removeHiddenElements: true,
      removeUnusedStyles: false,
      removeUnusedFonts: false,
      removeScripts: true,
      removeFrames: true,
      loadDeferredImages: true,
      networkTimeout: 8000,
      maxResourceSizeEnabled: true,
      maxResourceSize: 16,
      compressHTML: false,
      insertMetaCSP: false,
      insertMetaNoIndex: true,
      insertCanonicalLink: true,
      url: location.href
    }, {}, doc, win);
    const html = typeof pageData.content === "string" ? pageData.content : new TextDecoder().decode(new Uint8Array(pageData.content));
    return cleanArchivedHTML(html, location.href, doc.title || document.title || "Clipped page");
  }

  async function withTimeout(task, timeoutMs) {
    let timer;
    try {
      return await Promise.race([
        task(),
        new Promise((_, reject) => {
          timer = setTimeout(() => reject(new Error("SingleFile capture timed out")), timeoutMs);
        })
      ]);
    } finally {
      clearTimeout(timer);
    }
  }

  function isLargePage() {
    const nodeCount = document.getElementsByTagName("*").length;
    if (nodeCount > 40000) return true;
    const htmlSize = document.documentElement?.outerHTML?.length || 0;
    return htmlSize > 30000000;
  }

  function sanitizeDocument(doc) {
    const clone = doc.documentElement.cloneNode(true);
    const archived = document.implementation.createHTMLDocument(doc.title || document.title || "Clipped page");
    archived.documentElement.replaceWith(archived.importNode(clone, true));
    cleanArchiveDocument(archived, location.href);
    return serializeArchiveDocument(archived);
  }

  function sanitizeFragment(fragment) {
    const doc = selectionDocument(fragment);
    return sanitizeDocument(doc);
  }

  function selectionDocument(fragment) {
    const doc = document.implementation.createHTMLDocument(document.title || "Selection");
    const base = doc.createElement("base");
    base.href = location.href;
    doc.head.append(base);
    for (const node of document.head.querySelectorAll("meta, title, style, link[rel='stylesheet'], link[rel='preload'][as='style'], link[rel='icon'], link[rel='shortcut icon']")) {
      doc.head.append(node.cloneNode(true));
    }
    const wrapper = doc.createElement("main");
    wrapper.setAttribute("data-cairnfield-selection", "true");
    wrapper.append(fragment.cloneNode(true));
    doc.body.append(wrapper);
    const style = doc.createElement("style");
    style.textContent = `
      html, body { margin: 0; background: Canvas; color: CanvasText; }
      [data-cairnfield-selection="true"] { box-sizing: border-box; max-width: min(100%, 1040px); margin: 0 auto; padding: 24px; }
      [data-cairnfield-selection="true"] img, [data-cairnfield-selection="true"] video, [data-cairnfield-selection="true"] canvas, [data-cairnfield-selection="true"] svg { max-width: 100%; height: auto; }
    `;
    doc.head.append(style);
    return doc;
  }

  function cleanArchivedHTML(html, baseURL, title) {
    try {
      const parser = new DOMParser();
      const doc = parser.parseFromString(html, "text/html");
      if (!doc.title && title) doc.title = title;
      cleanArchiveDocument(doc, baseURL);
      return serializeArchiveDocument(doc);
    } catch {
      return html;
    }
  }

  function cleanArchiveDocument(doc, baseURL) {
    ensureBaseElement(doc, baseURL);
    doc.querySelectorAll("script, iframe, object, embed, template, noscript, link[rel='preload'], link[rel='modulepreload'], meta[http-equiv='Content-Security-Policy']").forEach((node) => node.remove());
    doc.querySelectorAll("[hidden], [aria-hidden='true']").forEach((node) => node.remove());
    doc.querySelectorAll("*").forEach((node) => {
      const style = node.getAttribute("style") || "";
      if (/\bdisplay\s*:\s*none\b/i.test(style) || /\bvisibility\s*:\s*hidden\b/i.test(style)) {
        node.remove();
        return;
      }
      for (const attr of [...node.attributes]) {
        const name = attr.name.toLowerCase();
        if (name.startsWith("on") || name === "srcdoc" || name === "nonce" || name === "integrity") {
          node.removeAttribute(attr.name);
        }
      }
      absolutizeArchiveURL(node, baseURL, "src");
      absolutizeArchiveURL(node, baseURL, "href");
      absolutizeArchiveURL(node, baseURL, "poster");
      if (node.hasAttribute("srcset")) node.removeAttribute("srcset");
    });
  }

  function ensureBaseElement(doc, baseURL) {
    if (!baseURL || doc.querySelector("base[href]")) return;
    const base = doc.createElement("base");
    base.href = baseURL;
    (doc.head || doc.documentElement).prepend(base);
  }

  function absolutizeArchiveURL(node, baseURL, attr) {
    const value = node.getAttribute?.(attr);
    if (!value || /^(data|blob|mailto|tel|javascript):/i.test(value) || !baseURL) return;
    try {
      node.setAttribute(attr, new URL(value, baseURL).href);
    } catch {
      // Leave invalid URLs as-is.
    }
  }

  function serializeArchiveDocument(doc) {
    return "<!doctype html>\n" + doc.documentElement.outerHTML;
  }

  function searchablePageText(root = document) {
    const chunks = [];
    const seen = new Set();
    const add = (value) => {
      const text = String(value || "").replace(/\s+/g, " ").trim();
      if (!text || seen.has(text)) return;
      seen.add(text);
      chunks.push(text);
    };
    add(root.title || document.title);
    add(root.body?.innerText || "");
    for (const node of root.querySelectorAll?.("img[alt], [aria-label], [title], input[placeholder], textarea[placeholder], input[value], button, a") || []) {
      add(node.getAttribute("alt"));
      add(node.getAttribute("aria-label"));
      add(node.getAttribute("title"));
      add(node.getAttribute("placeholder"));
      if ((node.tagName === "INPUT" || node.tagName === "TEXTAREA") && node.value) add(node.value);
      add(node.innerText || node.textContent);
    }
    return chunks.join("\n").slice(0, 250000);
  }

  window.cairnfieldSearchablePageText = () => searchablePageText(document);

  function styleSelectionFragment(fragment, selection) {
    const sourceElements = selectionSourceElements(selection);
    const clonedElements = Array.from(fragment.querySelectorAll("*"));
    const used = new Set();
    for (const cloned of clonedElements) {
      const source = matchingSourceElement(cloned, sourceElements, used);
      if (!source) continue;
      inlineComputedStyle(source, cloned);
      absolutizeURLs(source, cloned);
    }
    return fragment;
  }

  function selectionSourceElements(selection) {
    const rect = selectionRect(selection);
    const out = [];
    for (let i = 0; i < selection.rangeCount; i += 1) {
      const range = selection.getRangeAt(i);
      const root = range.commonAncestorContainer.nodeType === Node.ELEMENT_NODE ? range.commonAncestorContainer : range.commonAncestorContainer.parentElement;
      if (!root) continue;
      const candidates = root.matches?.("*") ? [root, ...root.querySelectorAll("*")] : Array.from(root.querySelectorAll("*"));
      for (const element of candidates) {
        if (!rangeIntersectsElement(range, element)) continue;
        if (rect && !rectsOverlap(rect, element.getBoundingClientRect())) continue;
        out.push(element);
      }
    }
    return out;
  }

  function matchingSourceElement(cloned, sourceElements, used) {
    const tag = cloned.tagName;
    for (let i = 0; i < sourceElements.length; i += 1) {
      if (used.has(i)) continue;
      if (sourceElements[i].tagName === tag) {
        used.add(i);
        return sourceElements[i];
      }
    }
    return null;
  }

  function rangeIntersectsElement(range, element) {
    try {
      return range.intersectsNode(element);
    } catch {
      return false;
    }
  }

  function rectsOverlap(a, b) {
    return b.width > 0 && b.height > 0 && b.right >= a.left && b.left <= a.left + a.width && b.bottom >= a.top && b.top <= a.top + a.height;
  }

  function inlineComputedStyle(source, target) {
    const computed = getComputedStyle(source);
    const properties = [
      "display", "box-sizing", "position", "float", "clear",
      "font", "font-family", "font-size", "font-style", "font-weight", "line-height", "letter-spacing", "text-align", "text-decoration", "text-transform", "white-space",
      "color", "background", "background-color", "background-image", "background-position", "background-size", "background-repeat",
      "border", "border-radius", "box-shadow", "outline",
      "margin", "padding", "width", "max-width", "min-width", "height", "max-height", "min-height",
      "list-style", "vertical-align", "object-fit", "object-position", "opacity"
    ];
    for (const property of properties) {
      const value = computed.getPropertyValue(property);
      if (value) target.style.setProperty(property, value, computed.getPropertyPriority(property));
    }
  }

  function absolutizeURLs(source, target) {
    for (const attr of ["src", "href", "poster"]) {
      const value = source.getAttribute?.(attr);
      if (!value) continue;
      try {
        target.setAttribute(attr, new URL(value, location.href).href);
      } catch {
        target.setAttribute(attr, value);
      }
    }
    if (source.currentSrc && target.tagName === "IMG") target.setAttribute("src", source.currentSrc);
    if (target.hasAttribute("srcset")) target.removeAttribute("srcset");
  }

  function selectionRect(selection) {
    const rects = [];
    for (let i = 0; i < selection.rangeCount; i += 1) {
      for (const rect of selection.getRangeAt(i).getClientRects()) {
        if (rect.width > 0 && rect.height > 0) rects.push(rect);
      }
    }
    if (rects.length === 0) return null;
    const left = Math.max(0, Math.min(...rects.map((rect) => rect.left)));
    const top = Math.max(0, Math.min(...rects.map((rect) => rect.top)));
    const right = Math.min(window.innerWidth, Math.max(...rects.map((rect) => rect.right)));
    const bottom = Math.min(window.innerHeight, Math.max(...rects.map((rect) => rect.bottom)));
    if (right <= left || bottom <= top) return null;
    return {
      left,
      top,
      width: right - left,
      height: bottom - top,
      viewportWidth: window.innerWidth,
      viewportHeight: window.innerHeight
    };
  }

  async function selectionHTML(fragment) {
    const doc = selectionDocument(fragment);
    try {
      return await withTimeout(() => captureSingleFile(doc, window), 30000);
    } catch {
      return sanitizeDocument(doc);
    }
  }

  async function settleSelectionPaint() {
    await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  }

  function serializableRange(range) {
    const start = nodePath(range.startContainer);
    const end = nodePath(range.endContainer);
    if (!start || !end) return null;
    return { start, startOffset: range.startOffset, end, endOffset: range.endOffset };
  }

  function nodePath(node) {
    const path = [];
    let current = node;
    while (current && current !== document) {
      const parent = current.parentNode;
      if (!parent) return null;
      path.unshift(Array.prototype.indexOf.call(parent.childNodes, current));
      current = parent;
    }
    return path;
  }

  function nodeFromPath(path) {
    let current = document;
    for (const index of path || []) {
      current = current.childNodes[index];
      if (!current) return null;
    }
    return current;
  }

  window.cairnfieldRestoreSelection = function (serializedRanges) {
    const selection = window.getSelection();
    if (!selection) return;
    selection.removeAllRanges();
    for (const item of serializedRanges || []) {
      const start = nodeFromPath(item.start);
      const end = nodeFromPath(item.end);
      if (!start || !end) continue;
      const range = document.createRange();
      range.setStart(start, Math.min(item.startOffset, start.length ?? start.childNodes.length));
      range.setEnd(end, Math.min(item.endOffset, end.length ?? end.childNodes.length));
      selection.addRange(range);
    }
  }

  window.cairnfieldCapturePage = async function () {
    const fallback = () => ({
      title: document.title,
      selectionText: String(window.getSelection?.() || "").trim(),
      searchText: searchablePageText(document),
      html: sanitizeDocument(document)
    });
    try {
      if (isLargePage()) {
        throw new Error("Page is too large for reliable inline SingleFile capture");
      }
      return {
        title: document.title,
        selectionText: String(window.getSelection?.() || "").trim(),
        searchText: searchablePageText(document),
        html: await withTimeout(captureSingleFile, 30000)
      };
    } catch (err) {
      const clip = fallback();
      clip.captureWarning = err.message || String(err);
      return clip;
    }
  };

  window.cairnfieldCaptureSelection = async function () {
    const selection = window.getSelection();
    if (!selection || selection.rangeCount === 0 || selection.toString().trim() === "") {
      return window.cairnfieldCapturePage();
    }
    const container = document.createDocumentFragment();
    const ranges = [];
    for (let i = 0; i < selection.rangeCount; i += 1) {
      const range = selection.getRangeAt(i);
      const serialized = serializableRange(range);
      if (serialized) ranges.push(serialized);
      container.append(range.cloneContents());
    }
    styleSelectionFragment(container, selection);
    const rect = selectionRect(selection);
    const selectionText = selection.toString().trim();
    selection.removeAllRanges();
    await settleSelectionPaint();
    const html = await selectionHTML(container);
    return {
      title: document.title,
      selectionText,
      searchText: selectionText || searchablePageText(document),
      html,
      previewRect: rect,
      selectionRanges: ranges
    };
  };

  window.cairnfieldCaptureSelectionImage = async function () {
    const selection = window.getSelection();
    if (!selection || selection.rangeCount === 0 || selection.toString().trim() === "") {
      throw new Error("Select text or page content first.");
    }
    const ranges = [];
    for (let i = 0; i < selection.rangeCount; i += 1) {
      const serialized = serializableRange(selection.getRangeAt(i));
      if (serialized) ranges.push(serialized);
    }
    const rect = selectionRect(selection);
    const selectionText = selection.toString().trim();
    selection.removeAllRanges();
    await settleSelectionPaint();
    return {
      title: document.title,
      selectionText,
      searchText: selectionText,
      previewRect: rect,
      selectionRanges: ranges
    };
  };
})();
