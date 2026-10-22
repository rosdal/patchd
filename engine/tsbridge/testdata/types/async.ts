// async return types
async function load(): Promise<string> {
  return "data";
}
async function twice() {
  const value = await load();
  //    ^? string
  return [value, value];
}
const p = twice();
//    ^? Promise<string[]>
export { p };
