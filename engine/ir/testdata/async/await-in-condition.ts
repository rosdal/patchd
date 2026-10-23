async function f(check: () => Promise<boolean>) {
  if (await check()) return "ok";
  return "no";
}
