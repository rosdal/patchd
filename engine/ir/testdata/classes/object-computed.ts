function f(k: string, v: unknown) {
  return { [k]: v, [`${k}_raw`]: String(v), ["fixed"]: 1 };
}
