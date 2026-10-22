function f(o: object, k: string) {
  return k in o && o instanceof Date;
}
