function f(headers: any) {
  const { "content-type": ct } = headers;
  return ct;
}
