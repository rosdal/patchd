function f(src: any) {
  let id, others;
  ({ id, ...others } = src);
  return others;
}
