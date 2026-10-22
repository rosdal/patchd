const key = "dynamic";
class C {
  [key]() {
    return 1;
  }
  ["literal"]() {
    return 2;
  }
  [Symbol.iterator]() {
    return [][Symbol.iterator]();
  }
}
