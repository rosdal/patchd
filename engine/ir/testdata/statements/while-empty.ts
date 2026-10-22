function f(it: { next(): boolean }) {
  while (it.next());
}
