function f(fn: Function, self: object, args: unknown[]) {
  fn.call(self, 1);
  fn.apply(self, args);
  return fn.bind(self);
}
