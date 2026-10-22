function f(xs: number[]) {
  let n = 0;
  for (const x of xs) {
    switch (x) {
      case 0:
        continue;
      case 1:
        break;
      default:
        n++;
    }
    n *= 2;
  }
  return n;
}
