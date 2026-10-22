function f(o: any) {
  const { id, secret, ...rest } = o;
  return rest;
}
