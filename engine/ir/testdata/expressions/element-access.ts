function f(o: any, k: string) {
  return [o[k], o["literal"], o[0], o[k + "x"]];
}
