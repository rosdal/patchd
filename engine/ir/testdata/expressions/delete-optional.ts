function f(o?: { a?: number }) {
  return delete o?.a;
}
