import type { AtelierApi } from "../preload/preload";

declare global {
  interface Window {
    atelier: AtelierApi;
  }
}

export {};
