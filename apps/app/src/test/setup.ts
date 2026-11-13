import { afterEach } from "vitest"

declare global {
  var __UI_SDK_STRICT__: boolean | undefined
}

// Node tests share this setup file; only jsdom files get the DOM stubs.
if (typeof document !== "undefined") {
  globalThis.__UI_SDK_STRICT__ = true

  // jsdom lacks these, and ui-sdk popups call them while opening.
  HTMLElement.prototype.showPopover = function () {}
  HTMLElement.prototype.hidePopover = function () {}
  Element.prototype.getAnimations = () => []
  Element.prototype.scrollIntoView = function () {}
  Element.prototype.checkVisibility = () => true
  Element.prototype.getClientRects = () => [] as unknown as DOMRectList
  document.elementFromPoint = () => null
  document.elementsFromPoint = () => []
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  window.matchMedia ??= (query: string) =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }) as MediaQueryList
  document.head.append(
    Object.assign(document.createElement("style"), {
      textContent: "[popover] { display: block !important }",
    })
  )

  const { cleanup } = await import("@testing-library/react")
  afterEach(cleanup)
}
