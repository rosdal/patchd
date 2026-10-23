async function f(a: Promise<string> | undefined) {
  return (a && (await a)) ?? "none";
}
