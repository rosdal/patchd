function f(n: number) {
  let s = "";
  switch (n) {
    case 1:
      s += "one";
    case 2:
      s += "two";
      break;
    case 3:
      s += "three";
  }
  return s;
}
