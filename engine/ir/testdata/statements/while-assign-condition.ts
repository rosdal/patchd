function f(re: RegExp, s: string) {
  let m;
  const out = [];
  while ((m = re.exec(s)) !== null) {
    out.push(m[0]);
  }
  return out;
}
