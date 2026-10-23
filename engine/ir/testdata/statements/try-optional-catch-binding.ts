function f() {
  try {
    risky();
  } catch {
    return false;
  }
  return true;
}
declare function risky(): void;
