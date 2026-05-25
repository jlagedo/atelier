import "@testing-library/dom";

// Some suites opt into the node environment (e.g. subprocess wire tests) where there is
// no window/DOM — the jsdom shims below don't apply there.
if (typeof window === "undefined") {
  // node environment: nothing to polyfill.
} else if (!window.matchMedia) {
  window.matchMedia = (query: string): MediaQueryList => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  });
}

// jsdom lacks ResizeObserver; Radix ScrollArea measures with it.
if (!("ResizeObserver" in globalThis)) {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}
