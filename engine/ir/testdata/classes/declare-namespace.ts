declare namespace Ext {
  function f(): void;
}
declare module "virtual" {
  export const v: number;
}
declare global {
  interface Window {
    app: unknown;
  }
}
export {};
