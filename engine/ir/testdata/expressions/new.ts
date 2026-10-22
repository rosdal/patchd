function f(url: string) {
  const u = new URL(url);
  const d = new Date();
  return [u, d];
}
