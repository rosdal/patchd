function check(x: unknown) {
  if (typeof x !== "string") {
    throw new TypeError("not a string");
  }
  return x;
}
