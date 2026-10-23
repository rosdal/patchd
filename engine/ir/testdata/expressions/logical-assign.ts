function f(o: { a?: string; b?: number; c?: boolean }) {
  o.a ??= "x";
  o.b ||= 1;
  o.c &&= false;
  return o;
}
