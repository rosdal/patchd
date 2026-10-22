function f() {
  let i = 0;
  for (;;) {
    if (i++ > 3) break;
  }
  return i;
}
