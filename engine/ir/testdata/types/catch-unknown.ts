export function f() {
  try {
    JSON.parse("x");
  } catch (e) {
    return e;
  }
}
