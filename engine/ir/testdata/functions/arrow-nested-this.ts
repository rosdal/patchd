function outer(this: { v: number }) {
  return () => () => this.v;
}
