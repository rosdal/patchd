function f(o?: { run(): number }) {
  return o?.run();
}
