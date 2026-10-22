function f(p: object) {
  return { __proto__: p, ["__proto__"]: 1, own: 2 };
}
