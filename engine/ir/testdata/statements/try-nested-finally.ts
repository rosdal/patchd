function f() {
  try {
    try {
      return a();
    } finally {
      b();
    }
  } finally {
    c();
  }
}
declare function a(): number;
declare function b(): void;
declare function c(): void;
