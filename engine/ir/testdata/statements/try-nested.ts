function f() {
  try {
    try {
      a();
    } catch (inner) {
      b(inner);
    }
    c();
  } catch (outer) {
    d(outer);
  }
}
declare function a(): void;
declare function b(e: unknown): void;
declare function c(): void;
declare function d(e: unknown): void;
