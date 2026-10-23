function f(o: any, k: string) {
  delete o.a;
  delete o[k];
  return delete o["b"];
}
