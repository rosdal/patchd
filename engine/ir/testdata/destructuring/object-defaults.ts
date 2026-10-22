function f(opts: any) {
  const { port = 80, host = "localhost" } = opts;
  return `${host}:${port}`;
}
