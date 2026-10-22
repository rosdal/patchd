function f(n: number) {
  let r = 0;
  switch (n) {
    case 1:
      r = 1;
      break;
    default:
      r = -1;
    case 2:
      r += 2;
  }
  return r;
}
