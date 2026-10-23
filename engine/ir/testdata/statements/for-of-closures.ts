function f(xs: string[]) {
  return xs.map((x) => {
    const fns = [];
    for (const y of xs) fns.push(() => x + y);
    return fns;
  });
}
