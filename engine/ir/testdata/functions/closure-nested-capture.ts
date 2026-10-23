function a() {
  let x = 1;
  function b() {
    function c() {
      return x;
    }
    return c;
  }
  return b;
}
