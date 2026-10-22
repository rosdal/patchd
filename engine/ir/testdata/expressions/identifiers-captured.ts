function f() {
  let n = 0;
  const get = () => n;
  n = 5;
  return get;
}
