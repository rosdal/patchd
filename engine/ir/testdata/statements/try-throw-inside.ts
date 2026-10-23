function f(x: number) {
  try {
    if (x < 0) throw new Error("negative");
    return x;
  } catch (e) {
    return 0;
  }
}
