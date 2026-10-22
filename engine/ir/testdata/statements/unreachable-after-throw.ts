function f() {
  throw new Error("x");
  cleanup();
}
declare function cleanup(): void;
