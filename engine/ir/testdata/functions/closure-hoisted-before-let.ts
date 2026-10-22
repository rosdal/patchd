function f() {
  function get() {
    return later;
  }
  let later = 5;
  return get;
}
