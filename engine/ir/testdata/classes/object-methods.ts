const svc = {
  name: "svc",
  run(x: number) {
    return this.name + x;
  },
  async load() {
    return 1;
  },
  *gen() {
    yield 1;
  },
};
