// The suite describes the app in Portuguese, the language of its first screens: the app itself opens in English.
try { localStorage.setItem("harflex:locale", "pt-BR"); } catch { /* jsdom always has storage */ }
import "@testing-library/jest-dom/vitest";

// React Flow measures its nodes; jsdom has no layout, so these stand in (as the React Flow testing guide suggests).
if (typeof globalThis.ResizeObserver === "undefined") {
  globalThis.ResizeObserver = class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
}
if (typeof globalThis.DOMMatrixReadOnly === "undefined") {
  globalThis.DOMMatrixReadOnly = class {
    m22: number;
    constructor(transform?: string) { const scale = /scale\(([1-9.])\)/.exec(transform ?? "")?.[1]; this.m22 = scale ? Number(scale) : 1; }
  } as unknown as typeof DOMMatrixReadOnly;
}
