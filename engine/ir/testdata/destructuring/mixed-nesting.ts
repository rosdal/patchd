function f(res: any) {
  const {
    data: [first, { id }],
  } = res;
  return [first, id];
}
