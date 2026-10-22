function f(xs: string[]) {
  const out = [];
  try {
    for (const x of xs) out.push(JSON.parse(x));
  } catch (e) {
    return [];
  }
  return out;
}
