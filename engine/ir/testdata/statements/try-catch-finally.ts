function f() {
  let r = 0;
  try {
    r = compute();
  } catch (e) {
    r = -1;
    log(e);
  } finally {
    cleanup();
  }
  return r;
}
declare function compute(): number;
declare function log(e: unknown): void;
declare function cleanup(): void;
