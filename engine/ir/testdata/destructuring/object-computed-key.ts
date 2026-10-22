function f(o: any, k: string) {
  const { [k]: v, ["lit"]: w } = o;
  return [v, w];
}
