declare function load(): Promise<Map<string, number>>;
export async function f() {
  const m = await load();
  return m.get("k");
}
