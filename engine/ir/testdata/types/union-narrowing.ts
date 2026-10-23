export function f(x: string | number) {
  if (typeof x === "string") {
    return x.toUpperCase();
  }
  return x + 1;
}
