function f(req: any) {
  const { body, query } = req;
  return [body, query];
}
