function f() {
  const out = [];
  for (var i = 0; i < 3; i++) out.push(() => i);
  return out;
}
